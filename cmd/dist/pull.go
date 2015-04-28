package main

import (
	"log"
	"strings"

	"golang.org/x/net/context"

	"github.com/codegangsta/cli"
	"github.com/docker/distribution/client"
)

var (
	commandPull = cli.Command{
		Name:   "pull",
		Usage:  "Pull and verify an image from a registry",
		Action: imagePull,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "r,registry",
				Value: "docker.io",
				Usage: "Registry to use (e.g.: localhost:5000)",
			},
			// Client Params
			// TrimHostname?
			// BaseNamespace File
			// Auth Configuration
			// Output Params
			// Where to store pull
		},
	}
)

func splitTag(ns string) (string, string) {
	nsSplit := ns
	lastSlash := strings.LastIndex(nsSplit, "/")
	if lastSlash > -1 {
		nsSplit = nsSplit[lastSlash:]
	}
	lastColon := strings.LastIndex(nsSplit, ":")
	if lastColon > -1 {
		return ns[:lastSlash+lastColon], nsSplit[lastColon+1:]
	}
	return ns, "latest"
}

func imagePull(c *cli.Context) {
	config := client.RepositoryClientConfig{
		TrimHostname:  true,
		AllowMirrors:  true,
		NamespaceFile: ".namespace.cfg",
		Header: map[string][]string{
			"User-Agent": {"docker/1.6.0 distribution-cli"},
		},
	}

	resolver, err := config.Resolver()
	if err != nil {
		log.Fatal(err)
	}

	for _, ns := range c.Args() {
		name, tag := splitTag(ns)
		log.Printf("Pulling %s %s", name, tag)
		nspace, err := resolver.Resolve(name)
		if err != nil {
			log.Fatal(err)
		}
		repo, err := nspace.Repository(context.Background(), name)
		if err != nil {
			log.Fatal(err)
		}

		ms := repo.Manifests()
		m1, err := ms.GetByTag(tag)
		if err != nil {
			log.Fatal(err)
		}

		//ls := repo.Layers()
		for _, layer := range m1.FSLayers {
			// Parse blobSum
			log.Printf("Pulling: %s", layer.BlobSum)
		}
		// Save manifest
		// Save each layer
		log.Printf("Manifest: %s", m1.Raw)

	}
}
