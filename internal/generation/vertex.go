package generation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"google.golang.org/api/aiplatform/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
)

type Vertex struct {
	api    *aiplatform.Service
	config Config
	parent string
}

func NewVertex(ctx context.Context, cfg Config, opts ...option.ClientOption) (*Vertex, error) {
	opts = append([]option.ClientOption{option.WithEndpoint("https://" + Location + "-aiplatform.googleapis.com/")}, opts...)
	api, err := aiplatform.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &Vertex{api: api, config: cfg, parent: "projects/" + cfg.Project + "/locations/" + Location}, nil
}

func (v *Vertex) Submit(ctx context.Context, job Job, preset Preset) (string, error) {
	if !preset.validProfile() || preset.EvaluateCompression && preset.ModeName() != "dynamic" {
		return "", &Rejected{Err: ErrInvalid}
	}
	args := []string{
		"--input-uri", preset.InputURI, "--input-sha256", preset.InputSHA256,
		"--output-prefix", "gs://" + v.config.OutputBucket + "/generated/" + job.ID + "/",
		"--job-id", job.ID, "--deadline", job.CreatedAt.Add(MaxRuntime).Format(time.RFC3339Nano),
		"--title", preset.Title, "--source", preset.Source, "--license", preset.License,
	}
	image, serviceAccount := v.config.Image, v.config.ServiceAccount
	machineType := "n1-standard-4"
	switch preset.ModeName() {
	case "live":
		if v.config.Live == nil {
			return "", &Rejected{Err: ErrInvalid}
		}
		image, serviceAccount = v.config.Live.Image, v.config.Live.ServiceAccount
		// Decode the 34 original camera streams without starving GPU input.
		machineType = "n1-standard-16"
		args = []string{
			"--input-uri", preset.InputURI, "--input-sha256", preset.InputSHA256,
			"--output-prefix", "gs://" + v.config.OutputBucket + "/live/" + job.ID + "/",
			"--catalog-bucket", v.config.Live.CatalogBucket, "--media-origin", v.config.MediaOrigin,
			"--session-id", job.ID, "--deadline", job.CreatedAt.Add(MaxRuntime).Format(time.RFC3339Nano),
			"--title", preset.Title, "--source", preset.Source, "--license", preset.License,
			"--source-seconds", "10", "--seed-available-after", "0.8",
			"--bootstrap-steps", "300", "--steps-per-update", "300", "--decode-workers", "16",
		}
	case "dynamic":
		args = append(args, "--mode", "dynamic", "--steps", "12000")
		if preset.EvaluateCompression {
			args = append(args, "--evaluate-compression")
		}
		if preset.QualityProfile != "" && preset.QualityProfile != "baseline" {
			args = append(args, "--quality-profile", preset.QualityProfile)
		}
	case "static":
		// Existing digest-pinned static workers do not accept --mode.
	default:
		return "", &Rejected{Err: ErrInvalid}
	}
	request := &aiplatform.GoogleCloudAiplatformV1CustomJob{
		DisplayName: "rangle-" + job.ID,
		Labels:      map[string]string{"application": "rangle", "environment": "presentation-stg", "rangle_job": job.ID},
		JobSpec: &aiplatform.GoogleCloudAiplatformV1CustomJobSpec{
			ServiceAccount: serviceAccount,
			Scheduling: &aiplatform.GoogleCloudAiplatformV1Scheduling{
				Strategy: "SPOT", Timeout: "1800s", DisableRetries: true,
			},
			WorkerPoolSpecs: []*aiplatform.GoogleCloudAiplatformV1WorkerPoolSpec{{
				ReplicaCount: 1,
				MachineSpec: &aiplatform.GoogleCloudAiplatformV1MachineSpec{
					MachineType: machineType, AcceleratorType: "NVIDIA_TESLA_T4", AcceleratorCount: 1,
				},
				DiskSpec: &aiplatform.GoogleCloudAiplatformV1DiskSpec{BootDiskType: "pd-standard", BootDiskSizeGb: 100},
				ContainerSpec: &aiplatform.GoogleCloudAiplatformV1ContainerSpec{
					ImageUri: image,
					Args:     args,
				},
			}},
		},
	}
	created, err := v.api.Projects.Locations.CustomJobs.Create(v.parent, request).Context(ctx).Do()
	if err != nil {
		var apiError *googleapi.Error
		if errors.As(err, &apiError) && apiError.Code >= 400 && apiError.Code < 500 && apiError.Code != 408 {
			return "", &Rejected{Err: err}
		}
		return "", err
	}
	if !v.validName(created.Name) {
		return "", fmt.Errorf("provider returned an invalid job name")
	}
	return created.Name, nil
}

func (v *Vertex) validName(name string) bool {
	for _, project := range []string{v.config.Project, v.config.ProjectNumber} {
		suffix, ok := strings.CutPrefix(name, "projects/"+project+"/locations/"+Location+"/customJobs/")
		if ok && suffix != "" && !strings.ContainsAny(suffix, "/?# :") {
			return true
		}
	}
	return false
}

func (v *Vertex) Find(ctx context.Context, id string) (string, error) {
	if !requestID.MatchString(id) {
		return "", fmt.Errorf("invalid job label")
	}
	response, err := v.api.Projects.Locations.CustomJobs.List(v.parent).
		Filter(`labels.application="rangle" AND labels.environment="presentation-stg" AND labels.rangle_job="` + id + `"`).PageSize(2).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	if len(response.CustomJobs) == 0 {
		return "", nil
	}
	if len(response.CustomJobs) > 1 || response.NextPageToken != "" {
		return "", fmt.Errorf("multiple provider jobs found for one generation request")
	}
	name := response.CustomJobs[0].Name
	if !v.validName(name) {
		return "", fmt.Errorf("provider returned an invalid job name")
	}
	return name, nil
}

func (v *Vertex) Status(ctx context.Context, name string) (State, error) {
	if !v.validName(name) {
		return "", fmt.Errorf("invalid provider job name")
	}
	job, err := v.api.Projects.Locations.CustomJobs.Get(name).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	switch job.State {
	case "JOB_STATE_PENDING", "JOB_STATE_QUEUED":
		return Queued, nil
	case "JOB_STATE_RUNNING":
		return Running, nil
	case "JOB_STATE_CANCELLING":
		return Cancelling, nil
	case "JOB_STATE_SUCCEEDED":
		return Succeeded, nil
	case "JOB_STATE_CANCELLED":
		return Cancelled, nil
	case "JOB_STATE_FAILED", "JOB_STATE_EXPIRED", "JOB_STATE_PARTIALLY_SUCCEEDED":
		return Failed, nil
	default:
		return "", fmt.Errorf("unexpected provider job state %q", job.State)
	}
}

func (v *Vertex) Cancel(ctx context.Context, name string) error {
	if !v.validName(name) {
		return fmt.Errorf("invalid provider job name")
	}
	_, err := v.api.Projects.Locations.CustomJobs.Cancel(name, &aiplatform.GoogleCloudAiplatformV1CancelCustomJobRequest{}).Context(ctx).Do()
	return err
}
