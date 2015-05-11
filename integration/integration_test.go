package integration

import (
	"io/ioutil"
	"os"
	"testing"

	"github.com/docker/distribution/context"
	"github.com/docker/distribution/registry/client"

	_ "github.com/docker/distribution/registry/storage/driver/filesystem"
)

func TestPull(t *testing.T) {
	ctx := context.Background()
	name := "integration-test/repo/pull"
	tag := "testtag"
	srcDir, srcRepo := createTmpRepository(ctx, name)
	if err := createRandomImage(srcRepo, tag); err != nil {
		t.Fatal(err)
	}
	dstDir, dstRepo := createTmpRepository(ctx, name)
	server := startRegistry(ctx, srcDir)
	defer server.Close()
	defer func() {
		if (!t.Failed() || os.Getenv("KEEP_ON_FAILURE") == "") && os.Getenv("ALWAYS_KEEP") == "" {
			os.RemoveAll(srcDir)
			os.RemoveAll(dstDir)
		} else {
			t.Logf("Directories not removed:\n%s\n%s", srcDir, dstDir)
		}
	}()

	clientConfig := &client.RepositoryConfig{AllowMirrors: true}
	repo, err := client.NewRepository(ctx, name, server.URL, clientConfig)
	if err != nil {
		t.Fatal(err)
	}

	if err := copyTag(ctx, dstRepo, repo, tag); err != nil {
		t.Fatal(err)
	}

	if err := checkDirectories(dstDir, srcDir); err != nil {
		t.Fatal(err)
	}
}

func TestPush(t *testing.T) {
	ctx := context.Background()
	name := "integration-test/repo/pull"
	tag := "testtag"
	srcDir, srcRepo := createTmpRepository(ctx, name)
	if err := createRandomImage(srcRepo, tag); err != nil {
		t.Fatal(err)
	}
	dstDir, _ := ioutil.TempDir("", "test-repository-")
	server := startRegistry(ctx, dstDir)
	defer server.Close()
	defer func() {
		if (!t.Failed() || os.Getenv("KEEP_ON_FAILURE") == "") && os.Getenv("ALWAYS_KEEP") == "" {
			os.RemoveAll(srcDir)
			os.RemoveAll(dstDir)
		} else {
			t.Logf("Directories not removed:\n%s\n%s", srcDir, dstDir)
		}
	}()

	clientConfig := &client.RepositoryConfig{AllowMirrors: true}
	repo, err := client.NewRepository(ctx, name, server.URL, clientConfig)
	if err != nil {
		t.Fatal(err)
	}

	if err := copyTag(ctx, repo, srcRepo, tag); err != nil {
		t.Fatal(err)
	}

	if err := checkDirectories(dstDir, srcDir); err != nil {
		t.Fatal(err)
	}
}
