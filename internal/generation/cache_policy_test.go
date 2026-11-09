package generation

import "testing"

func TestPublicArtifactCacheDirectives(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fields []string
		want   bool
	}{
		{"cdn TTL rewrite", []string{"public,max-age=86400"}, true},
		{"original immutable hint", []string{"public,max-age=31536000,immutable"}, true},
		{"case and whitespace", []string{" PUBLIC , max-age = 86400 "}, true},
		{"multiple fields", []string{"public", "max-age=86400"}, true},
		{"shared only", []string{"public,s-maxage=86400"}, true},
		{"shared overrides browser", []string{"public,max-age=0,s-maxage=86400"}, true},
		{"quoted TTL", []string{`public,max-age="86400"`}, true},
		{"quoted unrelated extension", []string{`public,max-age=86400,note="private,no-cache"`}, true},
		{"unrelated name", []string{"public,max-age=86400,not-private"}, true},
		{"missing header", nil, false},
		{"not public", []string{"not-public,max-age=86400,immutable"}, false},
		{"publicity", []string{"publicity,max-age=86400"}, false},
		{"missing TTL", []string{"public,immutable"}, false},
		{"zero TTL", []string{"public,max-age=0"}, false},
		{"negative TTL", []string{"public,max-age=-1"}, false},
		{"signed TTL", []string{"public,max-age=+1"}, false},
		{"overflow TTL", []string{"public,max-age=999999999999999999999"}, false},
		{"invalid TTL", []string{"public,max-age=tomorrow"}, false},
		{"private", []string{"public,max-age=86400,private"}, false},
		{"qualified private", []string{`public,max-age=86400,private="Authorization,Cookie"`}, false},
		{"no store separate field", []string{"public,max-age=86400", "no-store"}, false},
		{"no cache", []string{"public,max-age=86400,no-cache"}, false},
		{"qualified no cache", []string{`public,max-age=86400,no-cache="Set-Cookie"`}, false},
		{"shared zero overrides", []string{"public,max-age=86400,s-maxage=0"}, false},
		{"repeated TTL", []string{"public,max-age=0,max-age=86400"}, false},
		{"repeated shared TTL", []string{"public,s-maxage=86400", "s-maxage=86400"}, false},
		{"public inside extension", []string{`note="x,public,max-age=86400,y"`}, false},
		{"public has argument", []string{"public=1,max-age=86400"}, false},
		{"malformed quotes", []string{`public,max-age=86400,note="unterminated`}, false},
		{"malformed delimiter", []string{"public;private,max-age=86400"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := publicArtifactCache(tc.fields); got != tc.want {
				t.Fatalf("fields=%q got=%t want=%t", tc.fields, got, tc.want)
			}
		})
	}
}
