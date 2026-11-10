package generation

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Hosi121/rangle/internal/catalog"
	"github.com/Hosi121/rangle/internal/publication"
	"github.com/Hosi121/rangle/internal/publisher"
)

// Publication is separate from the transport catalog. These are validated,
// immutable viewer outputs, not synthetic stream.Manifest representations.
type Publication struct {
	JobID     string             `firestore:"job_id"`
	Clip      catalog.PublicClip `firestore:"clip"`
	Scene     ArtifactProof      `firestore:"scene"`
	Metrics   ArtifactProof      `firestore:"metrics"`
	Model     ArtifactProof      `firestore:"model"`
	ModelName string             `firestore:"model_name"`
}
type ArtifactProof struct {
	Bytes  int64  `firestore:"bytes"`
	SHA256 string `firestore:"sha256"`
}

type PublicationStore interface {
	Publications(context.Context) ([]Publication, error)
	SetPublication(context.Context, string, string, *Publication, time.Time) (Job, error)
}

var ErrArtifacts = errors.New("generated artifacts are not ready for publication")

func publicClip(job Job, origin string) catalog.PublicClip {
	p := job.Snapshot
	model := "3DGS"
	if p.ModeName() == "dynamic" {
		model = "4DGS"
	}
	return catalog.PublicClip{
		Context: p.Context.Clone(),
		ID:      "generated-" + job.ID, Revision: job.ID, Title: p.Title + "（生成した" + model + "）", Sport: p.Sport,
		Description: p.Description + "\n" + p.Source + " / " + p.License,
		Tags:        catalog.GeneratedTags(p.Sport, model), PreviewURL: p.PreviewURL,
		SceneURL: origin + "/generated/" + job.ID + "/scene.json",
	}
}

func (s *Service) SetPublication(ctx context.Context, owner, id string, published bool) (Job, error) {
	return s.SetPublicationFor(ctx, OperatorPrincipal(owner), id, published)
}
func (s *Service) SetPublicationFor(ctx context.Context, p Principal, id string, published bool) (Job, error) {
	permission := publisher.WithdrawClip
	if published {
		permission = publisher.PublishClip
	}
	if !p.allows(permission) {
		return Job{}, ErrPublisherForbidden
	}
	owner := p.ownerKey()
	if err := s.principalAccess(ctx, p); err != nil {
		return Job{}, err
	}
	if !requestID.MatchString(id) {
		return Job{}, ErrNotFound
	}
	job, err := s.Store.Get(ctx, id)
	if err != nil {
		return Job{}, err
	}
	if !p.owns(job) {
		return Job{}, ErrNotFound
	}
	if job.State != Succeeded || job.Snapshot.ModeName() == "live" || published && s.ReservedClipID != nil && s.ReservedClipID("generated-"+id) {
		return Job{}, ErrConflict
	}
	store, ok := s.Store.(PublicationStore)
	if !ok {
		return Job{}, fmt.Errorf("publication storage unavailable")
	}
	if job.Published == published {
		return job, nil
	}
	var publication *Publication
	if published {
		validated, err := s.validatePublication(ctx, job)
		if err != nil {
			return Job{}, fmt.Errorf("%w: %v", ErrArtifacts, err)
		}
		publication = &validated
	}
	result, err := store.SetPublication(ctx, owner, id, publication, s.now())
	if err == nil && s.Logger != nil {
		s.Logger.InfoContext(ctx, "generation publication changed", "job_id", id, "publisher_account_id", job.PublisherAccountID, "actor_kind", p.Kind, "actor_id", p.ID, "published", result.Published)
	}
	return result, err
}

func (s *Service) PublicClips(ctx context.Context) ([]catalog.PublicClip, error) {
	store, ok := s.Store.(PublicationStore)
	if !ok {
		return nil, fmt.Errorf("publication storage unavailable")
	}
	publications, err := store.Publications(ctx)
	if err != nil {
		return nil, err
	}
	if len(publications) > MaxAttempts {
		return nil, ErrBudget
	}
	clips := make([]catalog.PublicClip, 0, len(publications))
	for _, publication := range publications {
		if !requestID.MatchString(publication.JobID) || publication.Clip.ID != "generated-"+publication.JobID ||
			publication.Clip.SceneURL != s.Config.MediaOrigin+"/generated/"+publication.JobID+"/scene.json" ||
			publication.Clip.ValidateGenerated() != nil || !publication.validProofs() {
			return nil, fmt.Errorf("invalid generated publication registry")
		}
		clips = append(clips, publication.Clip)
	}
	return clips, nil
}

// CompletedClipExists matches public generated-detail eligibility independently
// of feed membership. It performs one known-ID lookup and never lists jobs.
func (s *Service) CompletedClipExists(ctx context.Context, id string) (bool, error) {
	_, err := s.CompletedClip(ctx, id)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (p Publication) validProofs() bool {
	if p.ModelName != "model.splat" && p.ModelName != "model.rgs" {
		return false
	}
	for index, proof := range []ArtifactProof{p.Scene, p.Metrics, p.Model} {
		maximum := int64(32 << 20)
		if index == 0 {
			maximum = 64 << 10
		}
		if index == 1 {
			maximum = 256 << 10
		}
		if proof.Bytes <= 0 || proof.Bytes > maximum || !checksum.MatchString(proof.SHA256) {
			return false
		}
	}
	return true
}

func proof(data []byte) ArtifactProof {
	sum := sha256.Sum256(data)
	return ArtifactProof{int64(len(data)), hex.EncodeToString(sum[:])}
}

func (s *Service) validatePublication(ctx context.Context, job Job) (Publication, error) {
	if !requestID.MatchString(job.ID) || job.Snapshot.ID != job.Preset {
		return Publication{}, ErrInvalid
	}
	cfg := s.Config
	cfg.Presets = []Preset{job.Snapshot}
	if err := cfg.Validate(); err != nil {
		return Publication{}, err
	}
	clip := publicClip(job, cfg.MediaOrigin)
	if err := clip.ValidateGenerated(); err != nil {
		return Publication{}, err
	}
	client := http.Client{Timeout: 25 * time.Second}
	if s.MediaClient != nil {
		client = *s.MediaClient
	}
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	client.Timeout = 25 * time.Second
	prefix := cfg.MediaOrigin + "/generated/" + job.ID + "/"
	get := func(name, encoding string, maximum int64) ([]byte, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, prefix+name, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("Accept-Encoding", encoding)
		response, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("generated %s returned HTTP %d", name, response.StatusCode)
		}
		if !publicArtifactCache(response.Header.Values("Cache-Control")) {
			return nil, fmt.Errorf("generated %s is not publicly cacheable", name)
		}
		if response.ContentLength > maximum || (response.Header.Get("Content-Encoding") != "" && response.Header.Get("Content-Encoding") != encoding) {
			return nil, fmt.Errorf("invalid generated %s response size or encoding", name)
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, maximum+1))
		if err != nil || len(data) == 0 || int64(len(data)) > maximum {
			return nil, fmt.Errorf("invalid generated %s size", name)
		}
		return data, nil
	}
	sceneBytes, err := get("scene.json", "identity", 64<<10)
	if err != nil {
		return Publication{}, err
	}
	var scene struct {
		Version    int      `json:"version"`
		Frames     []string `json:"frames"`
		FrameCount int      `json:"frameCount"`
		FrameRate  float64  `json:"frameRate"`
		Temporal   *struct {
			URL string `json:"url"`
		} `json:"temporal"`
		Attribution struct{ Label, Source, License string } `json:"attribution"`
	}
	if json.Unmarshal(sceneBytes, &scene) != nil || scene.Version != 1 || scene.FrameRate <= 0 || scene.FrameRate > 240 ||
		scene.Attribution.Label != job.Snapshot.Title || scene.Attribution.Source != job.Snapshot.Source || scene.Attribution.License != job.Snapshot.License {
		return Publication{}, fmt.Errorf("invalid generated scene")
	}
	name, encoding := "model.splat", "gzip"
	if job.Snapshot.ModeName() == "dynamic" {
		name, encoding = "model.rgs", "identity"
		if scene.Temporal == nil || scene.Temporal.URL != name || len(scene.Frames) != 0 || scene.FrameCount < 2 || scene.FrameCount > 300 {
			return Publication{}, fmt.Errorf("invalid generated temporal scene")
		}
	} else if scene.Temporal != nil || len(scene.Frames) != 1 || scene.Frames[0] != name || scene.FrameCount != 0 {
		return Publication{}, fmt.Errorf("invalid generated static scene")
	}
	metricsBytes, err := get("metrics.json", "identity", 256<<10)
	if err != nil {
		return Publication{}, err
	}
	var metrics struct {
		JobID           string          `json:"job_id"`
		Mode            json.RawMessage `json:"mode"`
		QualityProfile  string          `json:"quality_profile"`
		InputSHA256     string          `json:"input_sha256"`
		ModelBytes      json.RawMessage `json:"model_bytes"`
		ModelSHA256     json.RawMessage `json:"model_sha256"`
		SplatBytes      int64           `json:"splat_bytes"`
		SplatSHA256     string          `json:"splat_sha256"`
		Source, License string
	}
	if json.Unmarshal(metricsBytes, &metrics) != nil || metrics.JobID != job.ID ||
		metrics.InputSHA256 != job.Snapshot.InputSHA256 || metrics.Source != job.Snapshot.Source || metrics.License != job.Snapshot.License {
		return Publication{}, fmt.Errorf("invalid generated metrics")
	}
	expectedMode := job.Snapshot.ModeName()
	measuredMode := ""
	if len(metrics.Mode) > 0 {
		if json.Unmarshal(metrics.Mode, &measuredMode) != nil || measuredMode == "" {
			return Publication{}, fmt.Errorf("invalid generated metrics mode")
		}
	} else if expectedMode == "static" {
		measuredMode = "static"
	} else {
		return Publication{}, fmt.Errorf("generated metrics mode is required")
	}
	if measuredMode != expectedMode {
		return Publication{}, fmt.Errorf("generated metrics mode mismatch")
	}
	if expectedMode == "static" && (metrics.SplatBytes <= 0 || metrics.SplatBytes > 32<<20 || metrics.SplatBytes%32 != 0 || !checksum.MatchString(metrics.SplatSHA256)) {
		return Publication{}, fmt.Errorf("invalid generated splat size or checksum")
	}
	// Current workers declare the stored model pair. Earlier static workers
	// declare only the raw splat pair; never interpret null/partial values as
	// absent, and never use this compatibility path for temporal models.
	hasSize, hasHash := len(metrics.ModelBytes) > 0, len(metrics.ModelSHA256) > 0
	if hasSize != hasHash {
		return Publication{}, fmt.Errorf("generated model size and checksum must be paired")
	}
	var expectedModel *ArtifactProof
	if hasSize {
		expectedModel = &ArtifactProof{}
		if json.Unmarshal(metrics.ModelBytes, &expectedModel.Bytes) != nil || expectedModel.Bytes <= 0 || expectedModel.Bytes > 32<<20 ||
			json.Unmarshal(metrics.ModelSHA256, &expectedModel.SHA256) != nil || !checksum.MatchString(expectedModel.SHA256) {
			return Publication{}, fmt.Errorf("invalid generated model measurements")
		}
	} else if expectedMode != "static" {
		return Publication{}, fmt.Errorf("generated model measurements are required")
	}
	profile := job.Snapshot.QualityProfile
	if profile == "" {
		profile = "baseline"
	}
	measuredProfile := metrics.QualityProfile
	if measuredProfile == "" {
		measuredProfile = "baseline"
	}
	if profile != measuredProfile {
		return Publication{}, fmt.Errorf("generated quality profile mismatch")
	}
	modelLimit := int64(32 << 20)
	if expectedModel != nil {
		modelLimit = expectedModel.Bytes
	}
	model, err := get(name, encoding, modelLimit)
	if err != nil {
		return Publication{}, err
	}
	modelProof := proof(model)
	if expectedModel != nil && modelProof != *expectedModel {
		return Publication{}, fmt.Errorf("generated model checksum mismatch")
	}
	if name == "model.rgs" {
		if err := validateTemporalOutput(model, scene.FrameCount, scene.FrameRate); err != nil {
			return Publication{}, err
		}
	}
	if name == "model.splat" {
		reader, err := gzip.NewReader(bytes.NewReader(model))
		if err != nil {
			return Publication{}, err
		}
		defer reader.Close()
		hash := sha256.New()
		size, err := io.Copy(hash, io.LimitReader(reader, metrics.SplatBytes+1))
		if err != nil || size != metrics.SplatBytes || hex.EncodeToString(hash.Sum(nil)) != metrics.SplatSHA256 {
			return Publication{}, fmt.Errorf("generated splat checksum mismatch")
		}
	}
	return Publication{JobID: job.ID, Clip: clip, Scene: proof(sceneBytes), Metrics: proof(metricsBytes), Model: modelProof, ModelName: name}, nil
}

// Validate the pinned RGS1 container without rendering or proxying it. The
// compressed payload is bounded by the declared Gaussian count before hashing.
func validateTemporalOutput(data []byte, frames int, fps float64) error {
	if frames < 2 || frames > 32 {
		return fmt.Errorf("invalid generated temporal frame count")
	}
	return publication.ValidateTemporalModel(data, frames, fps)
}

// CompletedClip returns the same public metadata as known-ID clip detail. Feed
// withdrawal does not erase a completed clip's existing shared-link eligibility.
func (s *Service) CompletedClip(ctx context.Context, id string) (catalog.PublicClip, error) {
	raw, ok := strings.CutPrefix(id, "generated-")
	if !ok || !requestID.MatchString(raw) {
		return catalog.PublicClip{}, ErrNotFound
	}
	job, err := s.Store.Get(ctx, raw)
	if err != nil {
		return catalog.PublicClip{}, err
	}
	if job.ID != raw || job.State != Succeeded || job.Snapshot.ID == "" || job.Snapshot.ModeName() == "live" {
		return catalog.PublicClip{}, ErrNotFound
	}
	if err := s.publisherAccess(ctx, job.PublisherAccountID); err != nil {
		return catalog.PublicClip{}, err
	}
	return publicClip(job, s.Config.MediaOrigin), nil
}
