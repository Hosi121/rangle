// Package livesource reads one operator-selected live index. Media stays on the data plane.
package livesource

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"google.golang.org/api/option"
	"google.golang.org/api/storage/v1"
)

const MaxIndexBytes = 512 << 10

// Reader has no write methods or caller-controlled storage locations.
type Reader struct {
	read func(context.Context) ([]byte, error)
	root *os.Root
}

func (r *Reader) Read(ctx context.Context) ([]byte, error) { return r.read(ctx) }
func (r *Reader) Close() error {
	if r.root != nil {
		return r.root.Close()
	}
	return nil
}

func bounded(source io.Reader) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(source, MaxIndexBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > MaxIndexBytes {
		return nil, fmt.Errorf("live index exceeds %d bytes", MaxIndexBytes)
	}
	return body, nil
}

// Open accepts a fixed local index or gs://bucket/live/index.json. GCS uses ADC
// and a read-only scope; per-viewer requests cannot choose an object or origin.
func Open(ctx context.Context, location string, opts ...option.ClientOption) (*Reader, error) {
	if strings.HasPrefix(location, "gs:") {
		uri, err := url.Parse(location)
		if err != nil || uri.Scheme != "gs" || uri.User != nil || uri.RawQuery != "" || uri.Fragment != "" ||
			!regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,61}[a-z0-9]$`).MatchString(uri.Host) || uri.Path != "/live/index.json" || uri.RawPath != "" {
			return nil, fmt.Errorf("live GCS source must be gs://bucket/live/index.json")
		}
		opts = append([]option.ClientOption{option.WithScopes(storage.DevstorageReadOnlyScope)}, opts...)
		service, err := storage.NewService(ctx, opts...)
		if err != nil {
			return nil, fmt.Errorf("initialize live index reader: %w", err)
		}
		return &Reader{read: func(ctx context.Context) ([]byte, error) {
			call := service.Objects.Get(uri.Host, "live/index.json").Context(ctx)
			call.Header().Set("Cache-Control", "no-cache")
			response, err := call.Download()
			if err != nil {
				return nil, fmt.Errorf("read live index: %w", err)
			}
			defer response.Body.Close()
			return bounded(response.Body)
		}}, nil
	}
	if location == "" || strings.Contains(location, "://") {
		return nil, fmt.Errorf("live source must be a local index or fixed GCS object")
	}
	root, err := os.OpenRoot(filepath.Dir(location))
	if err != nil {
		return nil, err
	}
	name := filepath.Base(location)
	return &Reader{root: root, read: func(ctx context.Context) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		info, err := root.Lstat(name)
		if err != nil {
			return nil, err
		}
		if !info.Mode().IsRegular() || info.Size() > MaxIndexBytes {
			return nil, fmt.Errorf("live index must be a bounded regular file")
		}
		file, err := root.Open(name)
		if err != nil {
			return nil, err
		}
		defer file.Close()
		body, err := bounded(file)
		return body, errors.Join(err, ctx.Err())
	}}, nil
}
