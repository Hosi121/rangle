package catalog

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/delivery"
	"github.com/Hosi121/rangle/internal/stream"
)

type diskSnapshot struct {
	path, sha string
	data      []byte
	catalog   *Catalog
}

func persistSnapshot(t *testing.T, directory, assets, revision string, count int) diskSnapshot {
	t.Helper()
	entries := make([]clip, count)
	for i := range entries {
		id := fmt.Sprintf("clip-%d", i)
		payload := []byte(revision + "/" + id)
		assetURL := "/assets/" + id + "/" + revision + "/low/000.bin"
		writeSnapshotFile(t, filepath.Join(assets, filepath.FromSlash(assetURL[1:])), payload)
		entries[i] = clip{Title: id + " " + revision, Sport: "test", Description: "Synthetic transport bytes", Manifest: stream.Manifest{
			Version: 1, ClipID: id, Revision: revision, Codec: "synthetic-transport-v1",
			Representations: []stream.Representation{{ID: "low", BitrateBPS: int64(len(payload) * 8), PeakDecodedBytes: int64(len(payload)),
				Segments: []stream.Segment{{Index: 0, StartSeconds: 0, DurationSeconds: 1, URL: assetURL, SizeBytes: int64(len(payload)), Independent: true, SHA256: sourceDigest(payload)}},
			}},
		}}
	}
	data, err := json.Marshal(struct {
		Version int    `json:"version"`
		Clips   []clip `json:"clips"`
	}{1, entries})
	if err != nil {
		t.Fatal(err)
	}
	sha := sourceDigest(data)
	name := filepath.Join(directory, "snapshots", sha+".json")
	writeSnapshotFile(t, name, data)
	return diskSnapshot{name, sha, data, mustCatalog(t, string(data))}
}

func writeSnapshotFile(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
}

type snapshotProcess struct {
	url  string
	stop func()
}

// These are the actual server executable and TCP requests, with no Firestore,
// cloud credentials, browser, cache sleeps, or load generation involved.
func startSnapshotProcess(t *testing.T, binary, catalogPath, origin string) snapshotProcess {
	t.Helper()
	command := exec.Command(binary, "-listen", "127.0.0.1:0", "-catalog", catalogPath, "-asset-origin", origin, "-firestore-project=")
	logs, err := command.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	started := make(chan string, 1)
	logsDone := make(chan struct{})
	go func() {
		defer close(logsDone)
		scanner := bufio.NewScanner(logs)
		for scanner.Scan() {
			var event struct {
				Message string `json:"message"`
				Address string `json:"listen_addr"`
			}
			if json.Unmarshal(scanner.Bytes(), &event) == nil && event.Message == "server started" {
				started <- "http://" + event.Address
			}
		}
	}()
	done := make(chan struct{})
	var runError error
	go func() { runError = command.Wait(); close(done) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			if err := command.Process.Signal(os.Interrupt); err != nil {
				_ = command.Process.Kill()
			}
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = command.Process.Kill()
				<-done
			}
			<-logsDone
		})
	}
	t.Cleanup(stop)
	select {
	case address := <-started:
		return snapshotProcess{address, stop}
	case <-done:
		t.Fatalf("server exited before startup: %v", runError)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not announce startup")
	}
	return snapshotProcess{}
}

func snapshotFetch(t *testing.T, method, target string, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request, err := http.NewRequest(method, target, nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, data
}

func TestSnapshotServerLifecycle(t *testing.T) {
	directory, assets := t.TempDir(), t.TempDir()
	old := persistSnapshot(t, directory, assets, "v1", 3)
	newer := persistSnapshot(t, directory, assets, "v2", 4)
	mediaMux := http.NewServeMux()
	for _, snapshot := range []diskSnapshot{old, newer} {
		for _, manifest := range snapshot.catalog.Manifests() {
			h, err := delivery.NewFileHandler(manifest, assets, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = h.Close() })
			mediaMux.Handle(manifest.URL(), h)
			mediaMux.Handle("/assets/"+manifest.ClipID+"/"+manifest.Revision+"/", h)
		}
	}
	media := httptest.NewServer(mediaMux)
	t.Cleanup(media.Close)
	binary := filepath.Join(t.TempDir(), "rangle-server")
	buildContext, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(buildContext, "go", "build", "-race", "-o", binary, "../../cmd/rangle-server")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, output)
	}
	first := startSnapshotProcess(t, binary, old.path, media.URL)
	second := startSnapshotProcess(t, binary, old.path, media.URL)
	pageURL := feedPath + "?limit=1"
	oldResponse, cachedPage := snapshotFetch(t, "GET", first.url+pageURL, nil)
	otherResponse, otherPage := snapshotFetch(t, "GET", second.url+pageURL, nil)
	if oldResponse.Header.Get(catalogSHAHeader) != old.sha || !bytes.Equal(cachedPage, otherPage) || oldResponse.Header.Get("ETag") != otherResponse.Header.Get("ETag") {
		t.Fatal("same snapshot did not reproduce source identity, bytes and validator on another instance")
	}
	var oldPage struct {
		Clips      []PublicClip `json:"clips"`
		NextCursor string       `json:"next_cursor"`
	}
	if err := json.Unmarshal(cachedPage, &oldPage); err != nil || len(oldPage.Clips) != 1 || oldPage.NextCursor == "" {
		t.Fatalf("old page: %s (%v)", cachedPage, err)
	}
	oldCursorURL := pageURL + "&cursor=" + url.QueryEscape(oldPage.NextCursor)
	oldAssetURL := old.catalog.Manifests()[0].Representations[0].Segments[0].URL
	newAssetURL := newer.catalog.Manifests()[0].Representations[0].Segments[0].URL
	oldAssetResponse, oldAsset := snapshotFetch(t, "GET", media.URL+oldAssetURL, nil)
	newAssetResponse, newAsset := snapshotFetch(t, "GET", media.URL+newAssetURL, nil)
	if oldAssetResponse.StatusCode != 200 || newAssetResponse.StatusCode != 200 || bytes.Equal(oldAsset, newAsset) {
		t.Fatal("versioned media fixture did not contain distinct old and new bytes")
	}

	second.stop()
	second = startSnapshotProcess(t, binary, newer.path, media.URL)
	newResponse, newPage := snapshotFetch(t, "GET", second.url+pageURL, map[string]string{"If-None-Match": oldResponse.Header.Get("ETag"), "Cache-Control": "no-cache"})
	if newResponse.StatusCode != 200 || newResponse.Header.Get(catalogSHAHeader) != newer.sha || newResponse.Header.Get("ETag") == oldResponse.Header.Get("ETag") || bytes.Equal(cachedPage, newPage) {
		t.Fatal("new snapshot incorrectly validated an old cached first page")
	}
	var changedPage struct {
		NextCursor string `json:"next_cursor"`
	}
	if err := json.Unmarshal(newPage, &changedPage); err != nil || changedPage.NextCursor == "" {
		t.Fatal("new snapshot did not provide a continuation")
	}
	for _, cross := range []struct{ server, cursor, sha string }{
		{second.url, oldPage.NextCursor, newer.sha},
		{first.url, changedPage.NextCursor, old.sha},
	} {
		response, _ := snapshotFetch(t, "GET", cross.server+pageURL+"&cursor="+url.QueryEscape(cross.cursor), map[string]string{"If-None-Match": oldResponse.Header.Get("ETag")})
		if response.StatusCode != 400 || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("ETag") != "" || response.Header.Get(catalogSHAHeader) != cross.sha {
			t.Fatal("mixed instances accepted or cached a foreign-snapshot cursor")
		}
	}
	for _, server := range []snapshotProcess{first, second} {
		for _, method := range []string{"GET", "HEAD"} {
			response, body := snapshotFetch(t, method, server.url+pageURL, nil)
			conditional, data := snapshotFetch(t, method, server.url+pageURL, map[string]string{"If-None-Match": response.Header.Get("ETag")})
			if response.StatusCode != 200 || conditional.StatusCode != 304 || len(data) != 0 || conditional.Header.Get(catalogSHAHeader) != response.Header.Get(catalogSHAHeader) || method == "HEAD" && len(body) != 0 {
				t.Fatal("HEAD/304 did not retain the selected source identity")
			}
		}
	}

	// An origin change alone invalidates cursors and validators, with the same
	// selected source SHA. No request is sent to the illustrative origin.
	second.stop()
	second = startSnapshotProcess(t, binary, old.path, "https://other.example.test")
	response, _ := snapshotFetch(t, "GET", second.url+oldCursorURL, nil)
	if response.StatusCode != 400 {
		t.Fatal("changed media origin accepted the old cursor")
	}
	response, body := snapshotFetch(t, "GET", second.url+pageURL, map[string]string{"If-None-Match": oldResponse.Header.Get("ETag")})
	if response.StatusCode != 200 || response.Header.Get(catalogSHAHeader) != old.sha || response.Header.Get("ETag") == oldResponse.Header.Get("ETag") || !bytes.Contains(body, []byte("https://other.example.test/manifests/")) {
		t.Fatal("origin change lost static identity or reused the old representation")
	}

	// Reverting only catalog/origin configuration on the same executable restores
	// the prior first page, ETag and cursor while both media revisions remain.
	second.stop()
	second = startSnapshotProcess(t, binary, old.path, media.URL)
	response, body = snapshotFetch(t, "GET", second.url+pageURL, nil)
	if !bytes.Equal(cachedPage, body) || response.Header.Get("ETag") != oldResponse.Header.Get("ETag") || response.Header.Get(catalogSHAHeader) != old.sha {
		t.Fatal("rollback did not reproduce the previous snapshot")
	}
	response, _ = snapshotFetch(t, "GET", second.url+oldCursorURL, nil)
	if response.StatusCode != 200 {
		t.Fatal("rollback did not restore the old cursor contract")
	}
	for path, expected := range map[string][]byte{oldAssetURL: oldAsset, newAssetURL: newAsset} {
		response, body := snapshotFetch(t, "GET", media.URL+path, nil)
		if response.StatusCode != 200 || !bytes.Equal(body, expected) || !strings.Contains(response.Header.Get("Cache-Control"), "immutable") {
			t.Fatal("rollback deleted or changed a retained media revision")
		}
	}
	for _, snapshot := range []diskSnapshot{old, newer} {
		data, err := os.ReadFile(snapshot.path)
		if err != nil || !bytes.Equal(data, snapshot.data) {
			t.Fatal("rollout changed or removed an immutable catalog file")
		}
	}
	second.stop()

	corrupt := filepath.Join(directory, "corrupt.json")
	writeSnapshotFile(t, corrupt, []byte(`{"version":1,"clips":[`))
	invalid := filepath.Join(directory, "invalid.json")
	writeSnapshotFile(t, invalid, []byte(`{"version":2,"clips":[]}`))
	for _, name := range []string{filepath.Join(directory, "missing.json"), directory, corrupt, invalid} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		command := exec.CommandContext(ctx, binary, "-listen", "127.0.0.1:0", "-catalog", name, "-asset-origin", media.URL, "-firestore-project=")
		output, err := command.CombinedOutput()
		cancel()
		if err == nil || ctx.Err() == context.DeadlineExceeded || bytes.Contains(output, []byte("server started")) || !bytes.Contains(output, []byte("server failed")) {
			t.Fatalf("missing/corrupt snapshot did not fail before listening: %s (%v)", output, err)
		}
		response, _ := snapshotFetch(t, "GET", first.url+pageURL, nil)
		if response.StatusCode != 200 || response.Header.Get(catalogSHAHeader) != old.sha {
			t.Fatal("failed candidate changed the still-running prior instance")
		}
	}
}
