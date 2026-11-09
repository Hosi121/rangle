package publication

import (
	"encoding/json"
	"fmt"
	"github.com/Hosi121/rangle/internal/stream"
)

// ValidateLiveScene applies the same scene, camera and RGS1 checks as VOD
// publication without inventing a transport manifest or offline metrics file.
func ValidateLiveScene(sceneData, model []byte, title, source, license string, duration float64) error {
	if !finite(duration) || duration <= 0 {
		return fmt.Errorf("invalid live duration")
	}
	e := entry{Title: title, Manifest: stream.Manifest{Representations: []stream.Representation{{Segments: []stream.Segment{{DurationSeconds: duration}}}}}}
	if err := validateSceneData(sceneData, e, Approval{SourceURL: source, License: license}); err != nil {
		return err
	}
	var s scene
	if err := json.Unmarshal(sceneData, &s); err != nil {
		return err
	}
	_, err := temporalModel(model, s.FrameCount, s.FrameRate)
	return err
}
