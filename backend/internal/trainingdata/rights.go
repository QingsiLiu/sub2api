package trainingdata

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

type rightsSnapshot struct {
	bySubject map[string]RightsGrant
	loadedAt  time.Time
}

func rightsLookupKey(scopeType, scopeRef string) string {
	return strings.TrimSpace(scopeType) + "\x00" + strings.TrimSpace(scopeRef)
}

func ValidSubjectRef(value string) bool {
	value = strings.TrimSpace(value)
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func (m *Manager) SubjectRef(scopeType string, id int64) string {
	if m == nil || len(m.subjectHMACKey) == 0 || id <= 0 {
		return ""
	}
	mac := hmac.New(sha256.New, m.subjectHMACKey)
	_, _ = mac.Write([]byte(strings.TrimSpace(scopeType) + ":" + strconv.FormatInt(id, 10)))
	return hex.EncodeToString(mac.Sum(nil))
}

func (m *Manager) UserSubjectRef(userID int64) string {
	return m.SubjectRef("user", userID)
}

func (m *Manager) APIKeySubjectRef(apiKeyID int64) string {
	return m.SubjectRef("api_key", apiKeyID)
}

func (m *Manager) eligibleGrant(userSubjectRef, apiKeySubjectRef string, now time.Time) (RightsGrant, bool) {
	if m == nil {
		return RightsGrant{}, false
	}
	snapshot := m.rights.Load()
	if snapshot == nil {
		return RightsGrant{}, false
	}
	userGrant, userOK := snapshot.bySubject[rightsLookupKey("user", userSubjectRef)]
	keyGrant, keyOK := snapshot.bySubject[rightsLookupKey("api_key", apiKeySubjectRef)]
	for _, grant := range []RightsGrant{userGrant, keyGrant} {
		if grant.ScopeRef == "" {
			continue
		}
		if grant.RevokedAt != nil || (grant.ExpiresAt != nil && !grant.ExpiresAt.After(now)) {
			return RightsGrant{}, false
		}
		if grant.Status == RightsExcluded || grant.Status == RightsWithdrawn || grant.Status == RightsExpired || grant.Status == RightsLegalHold {
			return RightsGrant{}, false
		}
	}
	if keyOK && keyGrant.Status == RightsEligible && containsString(keyGrant.AllowedPurposes, "model_training") {
		return keyGrant, true
	}
	if userOK && userGrant.Status == RightsEligible && containsString(userGrant.AllowedPurposes, "model_training") {
		return userGrant, true
	}
	if !userOK && !keyOK {
		return RightsGrant{}, false
	}
	return RightsGrant{}, false
}

func (m *Manager) refreshRights(ctx context.Context) error {
	grants, err := loadActiveRights(ctx, m.db, time.Now().UTC())
	if err != nil {
		return err
	}
	next := &rightsSnapshot{bySubject: make(map[string]RightsGrant, len(grants)), loadedAt: time.Now().UTC()}
	for _, grant := range grants {
		key := rightsLookupKey(grant.ScopeType, grant.ScopeRef)
		if existing, ok := next.bySubject[key]; ok && existing.Version >= grant.Version {
			continue
		}
		next.bySubject[key] = grant
	}
	m.rights.Store(next)
	return nil
}

func containsString(values []string, wanted string) bool {
	wanted = strings.ToLower(strings.TrimSpace(wanted))
	for _, value := range values {
		if strings.ToLower(strings.TrimSpace(value)) == wanted {
			return true
		}
	}
	return false
}

type atomicRightsSnapshot struct {
	value atomic.Pointer[rightsSnapshot]
}

func (s *atomicRightsSnapshot) Load() *rightsSnapshot {
	if s == nil {
		return nil
	}
	return s.value.Load()
}

func (s *atomicRightsSnapshot) Store(value *rightsSnapshot) {
	if s == nil {
		return
	}
	s.value.Store(value)
}
