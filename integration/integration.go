package integration

import (
	"bytes"
	"crypto/rand"
	"fmt"
	"io"
	"io/ioutil"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"

	"github.com/Sirupsen/logrus"
	"github.com/docker/distribution"
	"github.com/docker/distribution/configuration"
	"github.com/docker/distribution/digest"
	"github.com/docker/distribution/manifest"
	"github.com/docker/distribution/registry/handlers"
	"github.com/docker/distribution/registry/storage"
	"github.com/docker/distribution/registry/storage/cache"
	"github.com/docker/distribution/registry/storage/driver/factory"
	"github.com/docker/libtrust"
	"golang.org/x/net/context"
)

var key libtrust.PrivateKey

func init() {
	var err error
	key, err = libtrust.GenerateECP256PrivateKey()
	if err != nil {
		panic(err)
	}

	logrus.SetLevel(logrus.DebugLevel)
}

func startRegistry(ctx context.Context, d string) *httptest.Server {
	// Start V2 registry
	config := configuration.Configuration{
		Storage: configuration.Storage{
			"filesystem": map[string]interface{}{
				"rootdirectory": d,
			},
		},
	}

	app := handlers.NewApp(ctx, config)

	return httptest.NewServer(app)
}

func createTmpRepository(ctx context.Context, name string) (string, distribution.Repository) {
	d, err := ioutil.TempDir("", "test-repository-")

	parameters := map[string]interface{}{
		"rootdirectory": d,
	}
	driver, err := factory.Create("filesystem", parameters)
	if err != nil {
		panic(err)
	}
	namespace := storage.NewRegistryWithDriver(ctx, driver, cache.NewInMemoryLayerInfoCache())
	repo, err := namespace.Repository(ctx, name)
	if err != nil {
		panic(err)
	}

	return d, repo
}

func createRandomImage(repo distribution.Repository, tag string) error {
	repoLS := repo.Layers()
	mnfst := &manifest.Manifest{
		Versioned: manifest.Versioned{
			SchemaVersion: 1,
		},
		Name:     repo.Name(),
		Tag:      tag,
		FSLayers: make([]manifest.FSLayer, 6),
	}

	for i := 0; i < 6; i++ {
		dgstr := digest.NewCanonicalDigester()
		upload, err := repoLS.Upload()
		if err != nil {
			return err
		}
		b := make([]byte, 2)
		rand.Reader.Read(b)
		size := int64(31 + i + int(uint32(b[0])*(uint32(b[1])<<5)))

		_, err = io.Copy(upload, io.TeeReader(io.LimitReader(rand.Reader, size), dgstr))
		if err != nil {
			return err
		}

		dgst := dgstr.Digest()

		if _, err := upload.Finish(dgst); err != nil {
			return err
		}

		mnfst.FSLayers[i].BlobSum = dgst
	}

	sm, err := manifest.Sign(mnfst, key)
	if err != nil {
		return err
	}

	if err := repo.Manifests().Put(sm); err != nil {
		return err
	}

	return nil
}

func copyTag(ctx context.Context, dst, src distribution.Repository, tag string) error {
	sm, err := src.Manifests().GetByTag(tag)
	if err != nil {
		return fmt.Errorf("manifest get: %s", err)
	}

	srcLS := src.Layers()
	dstLS := dst.Layers()
	for _, fsLayer := range sm.FSLayers {
		layer, err := srcLS.Fetch(fsLayer.BlobSum)
		if err != nil {
			return fmt.Errorf("fetch error: %s", err)
		}

		upload, err := dstLS.Upload()
		if err != nil {
			return fmt.Errorf("upload error: %s", err)
		}

		if _, err := io.Copy(upload, layer); err != nil {
			return fmt.Errorf("copy error: %s", err)
		}

		if _, err := upload.Finish(layer.Digest()); err != nil {
			return fmt.Errorf("finish error: %s", err)
		}

		upload.Close()
	}

	if err := dst.Manifests().Put(sm); err != nil {
		return fmt.Errorf("manifest put error: %s", err)
	}

	return nil
}

func checkDirectories(actual, expected string) error {
	return filepath.Walk(expected, diffWalker(expected, actual))
}

func checkFileInfo(actual, expected os.FileInfo) error {
	if actual.Name() != expected.Name() {
		return fmt.Errorf("mismatched name: %s, expected %s", actual.Name(), expected.Name())
	}
	if actual.IsDir() != expected.IsDir() {
		return fmt.Errorf("mismatched types")
	}

	if actual.Size() != expected.Size() {
		return fmt.Errorf("mismatched size: %d, expected %d", actual.Size(), expected.Size())
	}

	return nil
}

func diffWalker(base, other string) filepath.WalkFunc {
	return func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if path == base {
			return nil
		}

		if !strings.HasPrefix(path, base) {
			return fmt.Errorf("invalid path: %s", path)
		}

		path2 := other + path[len(base):]
		info2, err := os.Lstat(path2)
		if err != nil {
			return err
		}

		infoErr := checkFileInfo(info2, info)
		if infoErr != nil {
			return fmt.Errorf("Error comparing %s to %s: %s", path2, path, infoErr)
		}

		if !info.IsDir() {
			b1, err := ioutil.ReadFile(path)
			if err != nil {
				return err
			}
			b2, err := ioutil.ReadFile(path2)
			if err != nil {
				return err
			}
			if bytes.Compare(b1, b2) != 0 {
				return fmt.Errorf("Different file contents comparing %s to %s", path2, path)
			}
		}

		return nil
	}
}
