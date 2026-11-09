package publication

import (
	"testing"
)

func TestLiveSceneRetainsVODCameraCodecAndTimelineValidation(t *testing.T) {
	f := newFixture(t)
	scene, model := f.files[f.prefix+"scene.json"], f.files[f.prefix+"model.rgs"]
	if err := ValidateLiveScene(scene, model, "test clip", "https://source.example/dataset", "CC BY 4.0", .04); err != nil {
		t.Fatal(err)
	}
	for _, change := range []struct {
		title    string
		duration float64
		bytes    []byte
	}{
		{"different source title", .04, model}, {"test clip", .08, model}, {"test clip", .04, append(model, 0)},
	} {
		if err := ValidateLiveScene(scene, change.bytes, change.title, "https://source.example/dataset", "CC BY 4.0", change.duration); err == nil {
			t.Fatal("invalid live scene accepted")
		}
	}
}
