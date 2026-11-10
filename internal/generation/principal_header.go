package generation

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"time"
)

const principalHeader = "X-Rangle-Principal"

type principalAssertion struct {
	Version   int       `json:"v"`
	IssuedAt  int64     `json:"iat"`
	Principal Principal `json:"principal"`
}

// These assertions are not browser credentials. The manager accepts them only
// after verifying the pinned runtime's Google identity and audience. A short
// lifetime limits stale authorization; an already accepted request may finish
// after staff revocation. No cross-database transaction is implied.
func encodePrincipal(p Principal, now time.Time) (string, error) {
	if !p.valid() {
		return "", ErrPublisherForbidden
	}
	raw, err := json.Marshal(principalAssertion{1, now.Unix(), p})
	if err != nil || len(raw) > 3072 {
		return "", ErrPublisherForbidden
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func managerPrincipal(r *http.Request, now time.Time) (Principal, error) {
	values, present := r.Header[http.CanonicalHeaderKey(principalHeader)]
	if !present {
		// During a manager-first rollout, the old runtime can still forward an
		// operator ID. The reserved publisher: namespace cannot pass this path.
		owners := r.Header.Values(ownerHeader)
		if len(owners) != 1 || !operatorIdentifier.MatchString(owners[0]) {
			return Principal{}, ErrForbidden
		}
		return OperatorPrincipal(owners[0]), nil
	}
	if len(values) != 1 || len(values[0]) > 4096 || len(r.Header.Values(ownerHeader)) != 0 {
		return Principal{}, ErrPublisherForbidden
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(values[0])
	if err != nil {
		return Principal{}, ErrPublisherForbidden
	}
	var assertion principalAssertion
	if json.Unmarshal(raw, &assertion) != nil || assertion.Version != 1 || !assertion.Principal.valid() {
		return Principal{}, ErrPublisherForbidden
	}
	// Canonical encoding rejects duplicate/unknown fields and ambiguous values.
	canonical, err := json.Marshal(assertion)
	if err != nil || !bytes.Equal(raw, canonical) || assertion.IssuedAt < now.Add(-30*time.Second).Unix() || assertion.IssuedAt > now.Add(5*time.Second).Unix() {
		return Principal{}, ErrPublisherForbidden
	}
	return assertion.Principal, nil
}
