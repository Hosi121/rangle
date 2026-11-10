package publication

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
)

type scene struct {
	Version    int     `json:"version"`
	FrameRate  float64 `json:"frameRate"`
	FrameCount int     `json:"frameCount"`
	Camera     struct {
		Position             []float64 `json:"position"`
		LookAt               []float64 `json:"lookAt"`
		Up                   []float64 `json:"up"`
		FOV                  float64   `json:"fovYDegrees"`
		ReferenceImageHeight *int      `json:"referenceImageHeight,omitempty"`
		Intrinsics           *struct {
			Width  int     `json:"width"`
			Height int     `json:"height"`
			FX     float64 `json:"fx"`
			FY     float64 `json:"fy"`
			CX     float64 `json:"cx"`
			CY     float64 `json:"cy"`
		} `json:"intrinsics,omitempty"`
	} `json:"camera"`
	Attribution struct {
		Label   string `json:"label"`
		Source  string `json:"source"`
		License string `json:"license"`
	} `json:"attribution"`
	Temporal struct {
		URL string `json:"url"`
	} `json:"temporal"`
	Timeline *struct {
		PreviewURL          string   `json:"previewUrl"`
		PreviewStartSeconds *float64 `json:"previewStartSeconds"`
		SceneStartSeconds   *float64 `json:"sceneStartSeconds"`
	} `json:"timeline,omitempty"`
}

func finite(n float64) bool      { return !math.IsNaN(n) && !math.IsInf(n, 0) }
func little32(b []byte) uint32   { return binary.LittleEndian.Uint32(b) }
func float32At(b []byte) float64 { return float64(math.Float32frombits(little32(b))) }

func validateSceneData(sceneData []byte, e entry, a Approval) error {
	var s scene
	if err := decode(sceneData, &s); err != nil {
		return fmt.Errorf("scene schema: %w", err)
	}
	var optional struct {
		Camera   map[string]json.RawMessage `json:"camera"`
		Timeline json.RawMessage            `json:"timeline"`
	}
	if err := json.Unmarshal(sceneData, &optional); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(optional.Timeline), []byte("null")) {
		return fmt.Errorf("null scene timeline")
	}
	if s.Timeline != nil {
		t := s.Timeline
		if t.PreviewURL == "" || t.PreviewStartSeconds == nil || t.SceneStartSeconds == nil ||
			!finite(*t.PreviewStartSeconds) || *t.PreviewStartSeconds < 0 ||
			!finite(*t.SceneStartSeconds) || *t.SceneStartSeconds < 0 {
			return fmt.Errorf("invalid scene timeline anchors")
		}
	}
	for _, name := range []string{"intrinsics", "referenceImageHeight"} {
		if value, exists := optional.Camera[name]; exists && bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return fmt.Errorf("null scene camera option")
		}
	}
	if s.Version != 1 || s.FrameCount < 2 || s.FrameCount > 15000 || s.FrameRate <= 0 || s.FrameRate > 240 ||
		!finite(s.FrameRate) || s.Temporal.URL != "model.rgs" || s.Attribution.Label != e.Title ||
		s.Attribution.Source != a.SourceURL || s.Attribution.License != a.License {
		return fmt.Errorf("scene identity, attribution, or timeline mismatch")
	}
	c := s.Camera
	if len(c.Position) != 3 || len(c.LookAt) != 3 || len(c.Up) != 3 || c.FOV <= 0 || c.FOV >= 180 || !finite(c.FOV) {
		return fmt.Errorf("invalid scene camera")
	}
	same, zeroUp := true, true
	for i := range 3 {
		if !finite(c.Position[i]) || !finite(c.LookAt[i]) || !finite(c.Up[i]) {
			return fmt.Errorf("non-finite scene camera")
		}
		same = same && c.Position[i] == c.LookAt[i]
		zeroUp = zeroUp && c.Up[i] == 0
	}
	if same || zeroUp {
		return fmt.Errorf("degenerate scene camera")
	}
	if h := c.ReferenceImageHeight; h != nil && (*h < 64 || *h > 2160) {
		return fmt.Errorf("invalid scene reference height")
	}
	if p := c.Intrinsics; p != nil {
		if p.Width < 1 || p.Width > 16384 || p.Height < 1 || p.Height > 16384 || p.FX <= 0 || p.FY <= 0 ||
			p.CX < 0 || p.CX > float64(p.Width) || p.CY < 0 || p.CY > float64(p.Height) {
			return fmt.Errorf("invalid scene intrinsics")
		}
		for _, v := range []float64{p.FX / p.FY, 2 * p.FX / float64(p.Height), 2 * p.FY / float64(p.Height)} {
			f := float32(v)
			if f <= 0 || !finite(float64(f)) || !finite(float64(1/f)) {
				return fmt.Errorf("unstable scene intrinsics")
			}
		}
	}
	duration := float64(s.FrameCount-1) / s.FrameRate
	if math.Abs(e.Manifest.Representations[0].Segments[0].DurationSeconds-duration) > 1e-5 {
		return fmt.Errorf("transport and scene timelines differ")
	}
	return nil
}

func validateSceneModel(sceneData, model, metricsData []byte, e entry, a Approval) error {
	if err := validateSceneData(sceneData, e, a); err != nil {
		return err
	}
	var s scene
	if err := json.Unmarshal(sceneData, &s); err != nil {
		return err
	}
	duration := float64(s.FrameCount-1) / s.FrameRate
	raw, err := temporalModel(model, s.FrameCount, s.FrameRate)
	if err != nil {
		return err
	}
	// Evaluation keys may evolve; identity, wire and raw proofs remain required.
	// Validate duplicate keys/numbers throughout, then decode this common subset.
	var all map[string]json.RawMessage
	if err := decode(metricsData, &all); err != nil {
		return fmt.Errorf("metrics JSON: %w", err)
	}
	var metrics struct {
		Mode            string    `json:"mode"`
		ClipID          string    `json:"clip_id"`
		Source          string    `json:"source"`
		License         string    `json:"license"`
		InputSHA256     string    `json:"input_sha256"`
		ModelBytes      int64     `json:"model_bytes"`
		ModelSHA256     string    `json:"model_sha256"`
		RawModelBytes   int64     `json:"raw_model_bytes"`
		RawModelSHA256  string    `json:"raw_model_sha256"`
		FrameCount      int       `json:"frame_count"`
		FrameRate       float64   `json:"frame_rate"`
		DurationSeconds float64   `json:"duration_seconds"`
		FrameTimes      []float64 `json:"frame_times_seconds"`
	}
	if err := json.Unmarshal(metricsData, &metrics); err != nil {
		return fmt.Errorf("metrics: %w", err)
	}
	if metrics.Mode != "dynamic" || metrics.ClipID != a.ClipID || metrics.Source != a.SourceURL || metrics.License != a.License ||
		!validHash(metrics.InputSHA256) || metrics.ModelBytes != int64(len(model)) || metrics.ModelSHA256 != hash(model) ||
		metrics.RawModelBytes != int64(len(raw)) || metrics.RawModelSHA256 != hash(raw) ||
		metrics.FrameCount != s.FrameCount || metrics.FrameRate != s.FrameRate || math.Abs(metrics.DurationSeconds-duration) > 1e-5 ||
		len(metrics.FrameTimes) != s.FrameCount {
		return fmt.Errorf("metrics identity, proof, or timeline mismatch")
	}
	for i, t := range metrics.FrameTimes {
		if math.Abs(t-float64(i)/s.FrameRate) > 1e-5 {
			return fmt.Errorf("invalid metrics frame timeline")
		}
	}
	return nil
}

// Mirror the shipped viewer's RGS1 header and float32 primitive contract. Packed
// RANGL4Z1 gzip bytes are column-major, so transpose before validating rows.
func temporalModel(data []byte, frames int, fps float64) ([]byte, error) {
	const header, stride = 32, 56
	if len(data) < header || len(data) > maxModel {
		return nil, fmt.Errorf("invalid RGS1 size")
	}
	magic := string(data[:8])
	count := int64(little32(data[8:12]))
	rawSize := int64(header) + count*stride
	if (magic != "RANGL4D1" && magic != "RANGL4Z1") || count < 1 || count > 500000 || rawSize > maxModel || little32(data[12:16]) != stride ||
		int(little32(data[16:20])) != frames || float32At(data[20:24]) != float64(float32(fps)) ||
		!finite(float32At(data[20:24])) || !finite(float32At(data[24:28])) ||
		math.Abs(float32At(data[24:28])-float64(frames-1)/fps) > 1e-5 || little32(data[28:32]) != 0 {
		return nil, fmt.Errorf("invalid RGS1 header/timeline")
	}
	var raw []byte
	if magic == "RANGL4D1" {
		if int64(len(data)) != rawSize {
			return nil, fmt.Errorf("invalid RGS1 raw size")
		}
		raw = data
	} else {
		// RFC 1952 reserves the upper three flags. Go's gzip reader ignores
		// them, while the producer/viewer native decoders reject them.
		if len(data) < header+18 || data[header+3]&0xe0 != 0 {
			return nil, fmt.Errorf("invalid RGS1 gzip header")
		}
		compressed := bytes.NewReader(data[header:])
		reader, err := gzip.NewReader(compressed)
		if err != nil {
			return nil, fmt.Errorf("invalid RGS1 gzip: %w", err)
		}
		reader.Multistream(false) // Canonical Z1 is exactly one gzip member.
		columns, err := io.ReadAll(io.LimitReader(reader, count*stride+1))
		closeErr := reader.Close()
		if err != nil || closeErr != nil || int64(len(columns)) != count*stride || compressed.Len() != 0 {
			return nil, fmt.Errorf("invalid RGS1 gzip size/checksum/trailing bytes")
		}
		raw = make([]byte, rawSize)
		copy(raw, data[:header])
		copy(raw, "RANGL4D1")
		for column := int64(0); column < stride; column++ {
			for row := int64(0); row < count; row++ {
				raw[header+row*stride+column] = columns[column*count+row]
			}
		}
	}
	for row := int64(0); row < count; row++ {
		p := raw[header+row*stride : header+(row+1)*stride]
		for _, i := range []int{0, 1, 2, 3, 4, 5, 8, 9, 10, 11, 12, 13} {
			if !finite(float32At(p[4*i : 4*i+4])) {
				return nil, fmt.Errorf("non-finite RGS1 primitive")
			}
		}
		for _, i := range []int{3, 4, 5, 12} {
			if float32At(p[4*i:4*i+4]) <= 0 {
				return nil, fmt.Errorf("invalid RGS1 scale/lifetime")
			}
		}
		sigma := math.Float32frombits(little32(p[48:52]))
		opacity := float32At(p[52:56])
		if !finite(float64(1/sigma)) || opacity < 0 || opacity > 1 {
			return nil, fmt.Errorf("invalid RGS1 lifetime/opacity")
		}
	}
	return raw, nil
}

// EBML container validation verifies a WebM video track, bounded elements and
// its declared duration. It does not claim to decode every video frame.
func validateWebM(data []byte, frames int, fps float64) error {
	id, header, rest, err := ebmlElement(data, false)
	if err != nil || id != 0x1a45dfa3 || len(header) > 4096 {
		return fmt.Errorf("invalid WebM EBML header")
	}
	docType := false
	if err := ebmlChildren(header, func(id uint64, value []byte) error {
		if id == 0x4282 {
			if docType || string(value) != "webm" {
				return fmt.Errorf("invalid WebM DocType")
			}
			docType = true
		}
		return nil
	}); err != nil || !docType {
		return fmt.Errorf("invalid WebM DocType")
	}
	id, segment, tail, err := ebmlElement(rest, true)
	if err != nil || id != 0x18538067 || len(tail) != 0 {
		return fmt.Errorf("invalid WebM Segment")
	}
	var info, tracks bool
	var videoTrack uint64
	var clusters [][]byte
	scale, duration := float64(1000000), float64(0)
	err = ebmlChildren(segment, func(id uint64, value []byte) error {
		switch id {
		case 0x1549a966:
			if info {
				return fmt.Errorf("duplicate WebM Info")
			}
			info = true
			seen := map[uint64]bool{}
			return ebmlChildren(value, func(id uint64, v []byte) error {
				if (id == 0x2ad7b1 || id == 0x4489) && seen[id] {
					return fmt.Errorf("duplicate WebM time metadata")
				}
				seen[id] = true
				if id == 0x2ad7b1 {
					n, err := ebmlUint(v)
					if err != nil {
						return err
					}
					scale = float64(n)
				}
				if id == 0x4489 {
					switch len(v) {
					case 4:
						duration = float64(math.Float32frombits(binary.BigEndian.Uint32(v)))
					case 8:
						duration = math.Float64frombits(binary.BigEndian.Uint64(v))
					default:
						return fmt.Errorf("invalid WebM Duration")
					}
				}
				return nil
			})
		case 0x1654ae6b:
			if tracks {
				return fmt.Errorf("duplicate WebM Tracks")
			}
			tracks = true
			return ebmlChildren(value, func(id uint64, v []byte) error {
				if id != 0xae {
					return nil
				}
				var track, number uint64
				codec := ""
				seen := map[uint64]bool{}
				if err := ebmlChildren(v, func(id uint64, field []byte) error {
					if (id == 0x83 || id == 0xd7 || id == 0x86) && seen[id] {
						return fmt.Errorf("duplicate WebM track metadata")
					}
					seen[id] = true
					if id == 0x83 {
						n, err := ebmlUint(field)
						if err != nil {
							return err
						}
						track = n
					}
					if id == 0xd7 {
						n, err := ebmlUint(field)
						if err != nil {
							return err
						}
						number = n
					}
					if id == 0x86 {
						codec = string(field)
					}
					return nil
				}); err != nil {
					return err
				}
				if track != 1 || number == 0 || videoTrack != 0 || (codec != "V_VP8" && codec != "V_VP9" && codec != "V_AV1") {
					return fmt.Errorf("preview requires one VP8/VP9/AV1 video track")
				}
				videoTrack = number
				return nil
			})
		case 0x1f43b675:
			if len(value) == 0 {
				return fmt.Errorf("empty WebM Cluster")
			}
			clusters = append(clusters, value)
			if len(clusters) > frames {
				return fmt.Errorf("too many WebM clusters")
			}
		}
		return nil
	})
	seconds := duration * scale / 1e9
	if err != nil || !info || videoTrack == 0 || len(clusters) == 0 || !finite(seconds) || seconds <= 0 || scale <= 0 || math.Abs(seconds-float64(frames)/fps) > 0.05 {
		return fmt.Errorf("invalid WebM video container or duration")
	}
	count := 0
	for _, cluster := range clusters {
		var timestamp uint64
		var hasTimestamp bool
		type block struct {
			data []byte
			key  bool
		}
		var blocks []block
		if err := ebmlChildren(cluster, func(id uint64, value []byte) error {
			switch id {
			case 0xe7:
				if hasTimestamp {
					return fmt.Errorf("duplicate WebM cluster timestamp")
				}
				hasTimestamp = true
				n, err := ebmlUint(value)
				if err != nil {
					return err
				}
				timestamp = n
			case 0xa3:
				blocks = append(blocks, block{data: value})
			case 0xa0:
				var b block
				b.key = true
				if err := ebmlChildren(value, func(id uint64, value []byte) error {
					if id == 0xa1 {
						if b.data != nil {
							return fmt.Errorf("duplicate WebM Block")
						}
						b.data = value
					}
					if id == 0xfb {
						b.key = false
					}
					return nil
				}); err != nil {
					return err
				}
				if b.data == nil {
					return fmt.Errorf("missing WebM Block")
				}
				blocks = append(blocks, b)
			}
			if len(blocks) > frames {
				return fmt.Errorf("too many WebM blocks")
			}
			return nil
		}); err != nil {
			return err
		}
		if !hasTimestamp {
			return fmt.Errorf("missing WebM cluster timestamp")
		}
		for _, block := range blocks {
			b := block.data
			if len(b) < 5 || b[0] == 0 {
				return fmt.Errorf("truncated WebM video block")
			}
			n, bit := 1, byte(0x80)
			for b[0]&bit == 0 {
				n++
				bit >>= 1
			}
			if n > 8 || len(b) < n+4 {
				return fmt.Errorf("truncated WebM block header")
			}
			track := uint64(b[0] & (bit - 1))
			for i := 1; i < n; i++ {
				track = track<<8 | uint64(b[i])
			}
			flags := b[n+2]
			if track != videoTrack || flags&0x0e != 0 || (count == 0 && !block.key && flags&0x80 == 0) {
				return fmt.Errorf("invalid preview block track/lacing/visibility/keyframe")
			}
			t := (float64(timestamp) + float64(int16(binary.BigEndian.Uint16(b[n:n+2])))) * scale / 1e9
			if count >= frames || !finite(t) || math.Abs(t-float64(count)/fps) > .002 {
				return fmt.Errorf("preview frame timestamp differs from scene")
			}
			count++
		}
	}
	if count != frames {
		return fmt.Errorf("preview frame count differs from scene")
	}
	return nil
}

func ebmlChildren(data []byte, visit func(uint64, []byte) error) error {
	for len(data) > 0 {
		id, value, rest, err := ebmlElement(data, false)
		if err != nil {
			return err
		}
		if err := visit(id, value); err != nil {
			return err
		}
		data = rest
	}
	return nil
}

func ebmlElement(data []byte, allowUnknown bool) (uint64, []byte, []byte, error) {
	read := func(b []byte, strip bool, max int) (uint64, int, bool, error) {
		if len(b) == 0 || b[0] == 0 {
			return 0, 0, false, fmt.Errorf("truncated EBML integer")
		}
		n, bit := 1, byte(0x80)
		for b[0]&bit == 0 {
			n++
			bit >>= 1
		}
		if n > max || n > len(b) {
			return 0, 0, false, fmt.Errorf("invalid EBML integer")
		}
		v := uint64(b[0])
		if strip {
			v &= uint64(bit - 1)
		}
		for i := 1; i < n; i++ {
			v = v<<8 | uint64(b[i])
		}
		return v, n, strip && v == (uint64(1)<<(7*n))-1, nil
	}
	id, n, _, err := read(data, false, 4)
	if err != nil {
		return 0, nil, nil, err
	}
	size, m, unknown, err := read(data[n:], true, 8)
	if err != nil {
		return 0, nil, nil, err
	}
	body := data[n+m:]
	if unknown {
		if !allowUnknown {
			return 0, nil, nil, fmt.Errorf("unbounded EBML element")
		}
		size = uint64(len(body))
	}
	if size > uint64(len(body)) {
		return 0, nil, nil, fmt.Errorf("truncated EBML element")
	}
	return id, body[:size], body[size:], nil
}

func ebmlUint(data []byte) (uint64, error) {
	if len(data) == 0 || len(data) > 8 {
		return 0, fmt.Errorf("invalid EBML unsigned integer")
	}
	var n uint64
	for _, b := range data {
		n = n<<8 | uint64(b)
	}
	return n, nil
}
