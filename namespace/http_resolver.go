package namespace

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"golang.org/x/net/html"
)

type NSResolveActionEnum int

// Minimal interface of HTTP client used to perform discoveries over https.
type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

const (
	/* Add namespace entries to resulting list of entries and
	 * recursively fetch them (perform discovery on them). */
	NSResolveActionRecurse NSResolveActionEnum = iota
	// Don't add namespace entries to the resulting list.
	NSResolveActionIgnore
	/* Just add namespace entries to the resulting list.
	 * Don't perform discovery on them. */
	NSResolveActionPass
)

type htmlMetaTagEnum int

const (
	htmlMetaTagInvalid htmlMetaTagEnum = iota
	htmlMetaTagRegistryPull
	htmlMetaTagRegistryPush
	htmlMetaTagRegistry
	htmlMetaTagScope
	htmlMetaTagIndex
	htmlMetaTagNamespace
)

const (
	htmlMetaTagNameScope        = "docker-scope"
	htmlMetaTagNameRegistryPull = "docker-registry-pull"
	htmlMetaTagNameRegistryPush = "docker-registry-push"
	htmlMetaTagNameRegistry     = "docker-registry"
	htmlMetaTagNameIndex        = "docker-index"
	htmlMetaTagNameNamespace    = "docker-namespace"
)

var (
	reWhitespace = regexp.MustCompile(`\s+`)
)

type cacheEntry struct {
	created time.Time
	entries *Entries
}

func newCacheEntry(entries *Entries) cacheEntry {
	return cacheEntry{time.Now(), entries}
}

type httpResolver struct {
	client            HTTPClient
	resolverFactory   func(*Entries) Resolver
	nsResolveCallback func(string) NSResolveActionEnum
	expireAfter       time.Duration
	cache             map[string]cacheEntry
}

func (m htmlMetaTagEnum) String() string {
	switch m {
	case htmlMetaTagScope:
		return htmlMetaTagNameScope
	case htmlMetaTagRegistryPull:
		return htmlMetaTagNameRegistryPull
	case htmlMetaTagRegistryPush:
		return htmlMetaTagNameRegistryPush
	case htmlMetaTagRegistry:
		return htmlMetaTagNameRegistry
	case htmlMetaTagIndex:
		return htmlMetaTagNameIndex
	case htmlMetaTagNamespace:
		return htmlMetaTagNameNamespace
	default:
		panic(fmt.Sprintf("unexpected meta tag value %d", m))
	}
}

func (m htmlMetaTagEnum) ToActions() []string {
	res := make([]string, 0, 2)
	switch m {
	case htmlMetaTagRegistryPull:
		res = append(res, actionPull)
	case htmlMetaTagRegistryPush:
		res = append(res, actionPush)
	case htmlMetaTagIndex:
		res = append(res, actionIndex)
	case htmlMetaTagNamespace:
		res = append(res, actionNamespace)
	case htmlMetaTagRegistry:
		res = append(res, actionPull, actionPush)
	}
	return res
}

func parseHTMLMetaTagName(name string) (htmlMetaTagEnum, error) {
	switch strings.TrimSpace(name) {
	case htmlMetaTagNameScope:
		return htmlMetaTagScope, nil
	case htmlMetaTagNameRegistryPush:
		return htmlMetaTagRegistryPush, nil
	case htmlMetaTagNameRegistryPull:
		return htmlMetaTagRegistryPull, nil
	case htmlMetaTagNameRegistry:
		return htmlMetaTagRegistry, nil
	case htmlMetaTagNameIndex:
		return htmlMetaTagIndex, nil
	case htmlMetaTagNameNamespace:
		return htmlMetaTagNamespace, nil
	default:
		return htmlMetaTagInvalid, fmt.Errorf("unsupported meta tag name=%q", name)
	}
}

func parseHTMLMetaTag(z *html.Tokenizer, name string) (scope, []Entry, error) {
	var (
		args    []string
		entries []Entry
		tag     htmlMetaTagEnum
		err     error
	)
	for {
		attr, val, more := z.TagAttr()
		switch string(attr) {
		case "name":
			if tag != htmlMetaTagInvalid {
				return "", nil, fmt.Errorf("expected just one name attribute")
			}
			tag, err = parseHTMLMetaTagName(string(val))
			if err != nil {
				return "", nil, err
			}
			if actions := tag.ToActions(); len(actions) > 0 {
				entries = make([]Entry, len(actions))
				for i, action := range actions {
					entries[i] = Entry{action: action}
				}
			}
		case "content":
			args = reWhitespace.Split(strings.TrimSpace(string(val)), -1)
			if len(args) < 1 {
				return "", nil, fmt.Errorf("meta tag %s without any content")
			}
		default:
			return "", nil, fmt.Errorf("unrecognized meta tag attribute %s", string(attr))
		}
		if !more {
			break
		}
	}
	if tag == htmlMetaTagInvalid {
		return "", nil, fmt.Errorf("meta tag without name attribute")
	}
	if tag == htmlMetaTagScope {
		if len(args) != 1 {
			return "", nil, fmt.Errorf("unexpected arguments for scope meta tag: %q", strings.Join(args, " "))
		}
		scp, err := parseScope(args[0])
		if err != nil {
			return "", nil, err
		}
		return scp, nil, nil
	}
	if args == nil {
		return "", nil, fmt.Errorf("meta tag %s is missing content", tag.String())
	}
	for i := range entries {
		entries[i].args = args
	}
	return "", entries, nil
}

func parseHTMLHead(body io.Reader, name string) (*Entries, error) {
	var (
		parsedScope     scope
		readingMetaTags = false
		entries         = NewEntries()
		z               = html.NewTokenizer(body)
		err             error
	)
ParsingLoop:
	for {
		switch tokenType := z.Next(); tokenType {
		case html.ErrorToken:
			if z.Err() == io.EOF {
				break ParsingLoop
			}
			return nil, z.Err()
		case html.StartTagToken, html.SelfClosingTagToken:
			switch tagName, hasAttr := z.TagName(); string(tagName) {
			case "head":
				if readingMetaTags {
					return nil, fmt.Errorf("unexpected head tag")
				}
			case "meta":
				if !hasAttr {
					return nil, fmt.Errorf("meta tag empty")
				}
				scp, newEntries, err := parseHTMLMetaTag(z, name)
				if err != nil {
					return nil, err
				}
				if scp == "" {
					for _, entry := range newEntries {
						if err = entries.Add(entry); err != nil {
							return nil, err
						}
					}
				} else if parsedScope != "" {
					return nil, fmt.Errorf("multiple scopes defined")
				} else {
					parsedScope = scp
				}
			default:
				return nil, fmt.Errorf("unexpected html element %q", string(tagName))
			}
			readingMetaTags = true
		case html.EndTagToken:
			switch tagName, _ := z.TagName(); string(tagName) {
			case "head":
				break ParsingLoop
			case "meta":
			default:
				return nil, fmt.Errorf("unexpected tag %q", tagName)
			}
		default:
			continue ParsingLoop
		}
	}
	if !readingMetaTags || len(entries.entries) == 0 {
		return nil, fmt.Errorf("no entries found")
	}
	if parsedScope == "" {
		parsedScope, err = parseScope(name)
		if err != nil {
			return nil, fmt.Errorf("cannot use given name %q as scope: %v", name, err)
		}
	}
	for i := range entries.entries {
		entries.entries[i].scope = parsedScope
	}
	return entries, nil
}

/* Create base HTTP resolver.
 *
 * resolverFactory returns a resolver which will be called on fetched entries.
 * nsResolveCallback is called for every namespace extension.
 *		If not given, all scope extensions will be processed recursively.
 * expireAfter time interval saying how long to keep entries in cache
 */
func NewHttpResolver(client HTTPClient, resolverFactory func(*Entries) Resolver, nsResolveCallback func(string) NSResolveActionEnum, expireAfter time.Duration) Resolver {
	if client == nil {
		client = &http.Client{}
	}
	if resolverFactory == nil {
		resolverFactory = func(entries *Entries) Resolver {
			return NewSimpleResolver(entries, true)
		}
	}
	if nsResolveCallback == nil {
		nsResolveCallback = func(string) NSResolveActionEnum {
			return NSResolveActionRecurse
		}
	}
	return &httpResolver{
		client:            client,
		resolverFactory:   resolverFactory,
		nsResolveCallback: nsResolveCallback,
		expireAfter:       expireAfter,
		cache:             make(map[string]cacheEntry),
	}
}

func (hr *httpResolver) nameToURL(name string) string {
	return "https://" + name + "?docker-discovery=1"
}

func (hr *httpResolver) resolveEntries(es *Entries, visited map[string]struct{}, name string) error {
	cached, exists := hr.cache[name]
	if exists && !cached.created.Add(hr.expireAfter).After(time.Now()) {
		entries, err := hr.resolverFactory(cached.entries).Resolve(name)
		if err != nil {
			return err
		}
		es.Join(entries)
		return nil
	}
	if exists {
		delete(hr.cache, name)
	}
	resp, err := hr.client.Get(hr.nameToURL(name))
	// TODO: recursive resolver should allow for ignoring errors of scope extensions
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("discovery endpoint %q replied with: %s", name, resp.Status)
	}
	defer resp.Body.Close()
	// TODO: check content type

	entries, err := parseHTMLHead(resp.Body, name)
	if err != nil {
		return err
	}

	// handle scope extensions
	extensions := []string{}
	entriesToRemove := []*Entry{}
	for i, entry := range entries.entries {
		if entry.action == actionNamespace {
			argsToRemove := make(map[string]struct{})
			for _, arg := range entry.args {
				// When arg is not the name, also use additional scope
				if arg != name {
					scope, err := parseScope(arg)
					if err != nil {
						return err
					}
					switch hr.nsResolveCallback(name) {
					case NSResolveActionIgnore:
						argsToRemove[arg] = struct{}{}
					case NSResolveActionPass:
					case NSResolveActionRecurse:
						if !scope.Contains(name) {
							return errors.New("invalid extension: must extend ancestor scope")
						}
						if _, exists := visited[arg]; !exists {
							extensions = append(extensions, arg)
						}
					}
				}
			}
			if len(argsToRemove) > 0 {
				newArgs := make([]string, 0, len(entry.args)-len(argsToRemove))
				for _, arg := range entry.args {
					if _, exists := argsToRemove[arg]; !exists {
						newArgs = append(newArgs, arg)
					}
				}
				entry.args = newArgs
			}
			if len(entry.args) < 1 {
				entriesToRemove = append(entriesToRemove, &entries.entries[i])
			}
		}
	}

	for _, entryPtr := range entriesToRemove {
		entries.Remove(*entryPtr)
	}

	visited[name] = struct{}{}
	for _, ext := range extensions {
		if err = hr.resolveEntries(entries, visited, ext); err != nil {
			return err
		}
	}
	hr.cache[name] = newCacheEntry(entries)
	if entries, err = es.Join(entries); err != nil {
		return err
	}
	*es = *entries
	return nil
}

func (hr *httpResolver) Resolve(name string) (*Entries, error) {
	entries := NewEntries()
	if err := hr.resolveEntries(entries, make(map[string]struct{}), name); err != nil {
		return nil, err
	}
	return entries, nil
}
