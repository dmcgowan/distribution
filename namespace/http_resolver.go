package namespace

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"

	"github.com/Sirupsen/logrus"
	"golang.org/x/net/html"
)

// Minimal interface of HTTP client used to perform discoveries over https.
type HTTPClient interface {
	Get(url string) (*http.Response, error)
}

type NSResolveActionEnum int

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

type NSResolveActionCallback func(name string, namespace scope) NSResolveActionEnum

type HTTPResolverConfig struct {
	/* Client returns a resolver which will be called on fetched entries. */
	Client HTTPClient
	/* Factory creating resolver for fetched entries. */
	ResolverFactory func(*Entries) Resolver
	/* Don't terminate resolution because of errors during fetching from extension
	* namespaces. */
	IgnoreNSDiscoveryErrors bool
	/* NSResolveCallback is called for every namespace extension with a name being
	 * resolved. It shall return desired action. If not given, all namespace
	 * extensions which are ancestors to namespace being resolved will be processed
	 * recursively. Others will be ignored. Namespace can be empty denoting namespace
	 * entry without any arguments. */
	NSResolveCallback NSResolveActionCallback
}

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
			if len(args) == 1 && args[0] == "" {
				args = []string{}
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
	if args == nil && tag != htmlMetaTagNamespace {
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

type httpResolver struct {
	config *HTTPResolverConfig
}

/* Create base HTTP resolver.
 *
 * It uses discovery process to fetch scope entries. Multiple discovery
 * endpoints may be queried during single `Resolve()` call depending on scope's
 * namespace extensions and given callback.
 */
func NewHTTPResolver(config *HTTPResolverConfig) Resolver {
	if config == nil {
		config = &HTTPResolverConfig{}
	}
	if config.Client == nil {
		config.Client = &http.Client{}
	}
	if config.ResolverFactory == nil {
		config.ResolverFactory = func(entries *Entries) Resolver {
			return NewSimpleResolver(entries, true)
		}
	}
	if config.NSResolveCallback == nil {
		config.NSResolveCallback = func(name string, namespace scope) NSResolveActionEnum {
			if !namespace.Contains(name) {
				logrus.Debugf("Ignoring extension namespace %q which isn't an ancestor of %q", namespace, name)
				return NSResolveActionIgnore
			}
			return NSResolveActionRecurse
		}
	}
	return &httpResolver{
		config: config,
	}
}

func (hr *httpResolver) nameToURL(name string) string {
	return "https://" + name + "?docker-discovery=1"
}

func (hr *httpResolver) resolveEntries(es *Entries, visited map[string]struct{}, name string) error {
	resp, err := hr.config.Client.Get(hr.nameToURL(name))
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
	for i := range entries.entries {
		if entries.entries[i].action == actionNamespace {
			argsToRemove := make(map[string]struct{})
			for _, arg := range entries.entries[i].args {
				// When arg is not the name, also use additional scope
				if arg != name {
					scope, err := parseScope(arg)
					if err != nil {
						return err
					}
					switch hr.config.NSResolveCallback(name, scope) {
					case NSResolveActionIgnore:
						argsToRemove[arg] = struct{}{}
					case NSResolveActionPass:
					case NSResolveActionRecurse:
						if _, exists := visited[arg]; !exists {
							extensions = append(extensions, arg)
						}
					}
				}
			}
			if len(argsToRemove) > 0 {
				newArgs := make([]string, 0, len(entries.entries[i].args)-len(argsToRemove))
				for _, arg := range entries.entries[i].args {
					if _, exists := argsToRemove[arg]; !exists {
						newArgs = append(newArgs, arg)
					}
				}
				entries.entries[i].args = newArgs
			}
			if len(entries.entries[i].args) < 1 {
				if hr.config.NSResolveCallback(name, "") == NSResolveActionIgnore {
					entriesToRemove = append(entriesToRemove, &entries.entries[i])
				}
			}
		}
	}

	for _, entryPtr := range entriesToRemove {
		entries.Remove(*entryPtr)
	}

	visited[name] = struct{}{}
	for _, ext := range extensions {
		if err = hr.resolveEntries(entries, visited, ext); err != nil {
			if hr.config.IgnoreNSDiscoveryErrors {
				logrus.Warnf("Ignoring discovery error for extension namespace %q: %v", ext, err)
			} else {
				return err
			}
		}
	}
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
