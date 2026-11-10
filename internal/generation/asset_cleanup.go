package generation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"google.golang.org/api/compute/v1"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/option"
	"google.golang.org/api/storage/v1"
)

var ownedPrefix = regexp.MustCompile(`^((generated|live)/[a-f0-9]{32}/|live-index/[a-f0-9]{32}\.json)$`)
var operationName = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,200}$`)

type CloudAssetCleanup struct {
	storage                       *storage.Service
	compute                       *compute.Service
	project, bucket, host, urlMap string
}

func NewCloudAssetCleanup(ctx context.Context, cfg Config, urlMap string, opts ...option.ClientOption) (*CloudAssetCleanup, error) {
	if cfg.Validate() != nil || !identifier.MatchString(urlMap) {
		return nil, ErrInvalid
	}
	u, err := url.Parse(cfg.MediaOrigin)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.Port() != "" {
		return nil, ErrInvalid
	}
	objects, err := storage.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	edge, err := compute.NewService(ctx, opts...)
	if err != nil {
		return nil, err
	}
	return &CloudAssetCleanup{storage: objects, compute: edge, project: cfg.Project, bucket: cfg.OutputBucket, host: u.Hostname(), urlMap: urlMap}, nil
}

func (c *CloudAssetCleanup) Purge(ctx context.Context, prefix string) (bool, error) {
	if !ownedPrefix.MatchString(prefix) {
		return false, ErrInvalid
	}
	// Versioning must not resurrect an older live version. Soft-delete retention
	// remains bucket policy; these versions are removed from serving, not restored.
	page, err := c.storage.Objects.List(c.bucket).Prefix(prefix).Versions(true).MaxResults(100).
		Fields("items(name,generation),nextPageToken").Context(ctx).Do()
	if err != nil {
		return false, err
	}
	if len(page.Items) == 0 {
		if page.NextPageToken != "" {
			return false, ErrConflict
		}
		return true, nil
	}
	for _, o := range page.Items {
		if o == nil || !ownedObject(prefix, o.Name) || o.Generation <= 0 {
			return false, ErrConflict
		}
	}
	for _, o := range page.Items {
		err := c.storage.Objects.Delete(c.bucket, o.Name).Generation(o.Generation).IfGenerationMatch(o.Generation).Context(ctx).Do()
		var api *googleapi.Error
		if err != nil && !(errors.As(err, &api) && api.Code == 404) {
			return false, err
		}
	}
	// Only a subsequent empty listing confirms completion; bounded work per call.
	return false, nil
}

func ownedObject(prefix, name string) bool {
	if strings.HasPrefix(prefix, "live-index/") {
		return name == prefix
	}
	return strings.HasPrefix(name, prefix)
}

func invalidationID(rid string, index int) string {
	h := sha256.Sum256([]byte(fmt.Sprintf("publisher-deletion:%s:%d", rid, index)))
	h[6] = (h[6] & 15) | 0x50
	h[8] = (h[8] & 63) | 0x80
	s := hex.EncodeToString(h[:16])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func (c *CloudAssetCleanup) Invalidate(ctx context.Context, prefix, rid string) (string, error) {
	if !ownedPrefix.MatchString(prefix) || !regexpUUID.MatchString(rid) {
		return "", ErrInvalid
	}
	op, err := c.compute.UrlMaps.InvalidateCache(c.project, c.urlMap, &compute.CacheInvalidationRule{Host: c.host, Path: invalidationPath(prefix)}).RequestId(rid).Context(ctx).Do()
	if err != nil {
		return "", err
	}
	if !operationName.MatchString(op.Name) {
		return "", ErrConflict
	}
	return op.Name, nil
}

var regexpUUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-5[a-f0-9]{3}-[89ab][a-f0-9]{3}-[a-f0-9]{12}$`)

func (c *CloudAssetCleanup) InvalidationDone(ctx context.Context, name string) (bool, error) {
	if !operationName.MatchString(name) {
		return false, ErrInvalid
	}
	op, err := c.compute.GlobalOperations.Get(c.project, name).Context(ctx).Do()
	if err != nil {
		return false, err
	}
	if op.HttpErrorStatusCode > 0 || op.Error != nil && len(op.Error.Errors) > 0 {
		return false, fmt.Errorf("CDN invalidation failed")
	}
	return op.Status == "DONE", nil
}

func invalidationPath(prefix string) string {
	if strings.HasPrefix(prefix, "live-index/") {
		return "/" + prefix
	}
	return "/" + prefix + "*"
}
