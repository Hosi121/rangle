package integration_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Hosi121/rangle/internal/abr"
	"github.com/Hosi121/rangle/internal/delivery"
	"github.com/Hosi121/rangle/internal/stream"
)

func TestAdaptiveSelectionFetchesAdvertisedAssets(t *testing.T) {
	m, assets := delivery.DemoFixture()
	handler, err := delivery.NewHandler(m, assets)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	client := server.Client()
	client.Timeout = 5 * time.Second

	// Read the actual wire format rather than passing fixture metadata directly
	// into the controller. Network samples below are injected, not measurements
	// of the loopback server or a claim about real 4DGS bandwidth.
	var manifest stream.Manifest
	if err := json.Unmarshal(fetch(t, client, server.URL+m.URL()), &manifest); err != nil {
		t.Fatal(err)
	}
	config := abr.DefaultConfig()
	config.MaxDecodedBytes = 128 * 1024 // The high rendition cannot fit.
	config.StartupBufferSeconds = 0.25
	config.UpshiftConfirmations = 2
	config.Estimator.FastHalfLifeSeconds = 0.1
	config.Estimator.SlowHalfLifeSeconds = 0.2
	controller, err := abr.NewController(manifest, config)
	if err != nil {
		t.Fatal(err)
	}
	current := ""
	cached := make(map[string]bool)
	wantQualities := []string{"low", "low", "medium", "low"}
	for index, want := range wantQualities {
		if index > 0 {
			sample := abr.Sample{Bytes: 12500, DurationSeconds: 1}
			if index == 3 {
				sample = abr.Sample{Bytes: 8, DurationSeconds: 4}
			}
			if err := controller.Observe(sample); err != nil {
				t.Fatal(err)
			}
		}
		decision, err := controller.Select(abr.Request{
			SegmentIndex: index, CurrentID: current, BufferSeconds: 2, InitCached: cached,
		})
		if err != nil {
			t.Fatal(err)
		}
		if decision.RepresentationID != want {
			t.Fatalf("segment %d: quality %q, want %q (reason %s)", index, decision.RepresentationID, want, decision.Reason)
		}
		representation := manifest.Representations[decision.RepresentationIndex]
		if representation.PeakDecodedBytes > config.MaxDecodedBytes {
			t.Fatal("selection exceeded the memory budget")
		}
		var downloaded int64
		if !cached[representation.ID] && representation.InitURL != "" {
			initialization := fetch(t, client, server.URL+representation.InitURL)
			if int64(len(initialization)) != representation.InitSizeBytes {
				t.Fatal("initialization size does not match manifest")
			}
			downloaded += int64(len(initialization))
			cached[representation.ID] = true
		}
		segment := representation.Segments[index]
		payload := fetch(t, client, server.URL+segment.URL)
		downloaded += int64(len(payload))
		sum := sha256.Sum256(payload)
		if hex.EncodeToString(sum[:]) != segment.SHA256 {
			t.Fatal("retrieved bytes do not match the advertised segment hash")
		}
		if downloaded != decision.RequiredBytes {
			t.Fatalf("downloaded %d bytes, controller budgeted %d", downloaded, decision.RequiredBytes)
		}
		if index == 3 && decision.WithinDeliveryBudget {
			t.Fatal("collapsed bandwidth was incorrectly advertised as sustainable")
		}
		current = decision.RepresentationID
	}

	config.MaxDecodedBytes = 1
	unusable, err := abr.NewController(manifest, config)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unusable.Select(abr.Request{}); !errors.Is(err, abr.ErrNoFeasibleRepresentation) {
		t.Fatalf("impossible memory budget: got %v", err)
	}
}

func fetch(t *testing.T, client *http.Client, url string) []byte {
	t.Helper()
	response, err := client.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET %s: %s", url, response.Status)
	}
	data, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
