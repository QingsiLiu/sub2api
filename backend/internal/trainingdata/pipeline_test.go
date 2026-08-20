package trainingdata

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCurateAndDeliverLocalCaptureBundle(t *testing.T) {
	root := t.TempDir()
	captureRoot := filepath.Join(root, "captures")
	captureDir := filepath.Join(captureRoot, "capture-1")
	require.NoError(t, os.MkdirAll(filepath.Join(captureDir, "client"), 0o700))
	requestBody := []byte(`{"model":"model-a","messages":[{"role":"system","content":"do not deliver"},{"role":"user","content":"email user@example.com"}]}`)
	responseBody := []byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`)
	require.NoError(t, os.WriteFile(filepath.Join(captureDir, "client/request.body"), requestBody, 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(captureDir, "client/response.body"), responseBody, 0o600))
	startedAt := time.Date(2026, 8, 20, 1, 2, 3, 0, time.UTC)
	finishedAt := startedAt.Add(time.Second)
	capture := Manifest{
		SchemaVersion: "capture-v1", CaptureID: "capture-1", UserSubjectRef: "user-ref",
		APIKeySubjectRef: "key-ref", RightsID: "rights-1", RightsVersion: 1,
		RightsStatus: RightsEligible, StartedAt: startedAt, FinishedAt: &finishedAt,
		Method: "POST", Route: "/v1/chat/completions", Protocol: "openai_chat_completions",
		ClientModel: "model-a", ClientRequestFile: "client/request.body",
		ClientResponse: HeaderSnapshot{Status: 200}, ClientResponseFile: "client/response.body",
		CaptureComplete: true, RedactionVersion: RedactionVersion,
		Attempts: []AttemptManifest{{Model: "upstream-a", HTTPStatus: 200, Complete: true}},
	}
	require.NoError(t, writeJSONFile(filepath.Join(captureDir, "manifest.json"), capture))
	grant := RightsGrant{
		RightsID: "rights-1", ScopeType: "user", ScopeRef: "user-ref", Version: 1,
		Status: RightsEligible, AllowedPurposes: []string{"model_training"},
		AllowedDatasetTypes: []string{"chat"}, AllowedRecipients: []string{"buyer-a"},
		EffectiveAt: startedAt.Add(-time.Hour),
	}

	curatedDir := filepath.Join(root, "curated-v1")
	curated, err := CurateLocalCaptures(captureRoot, curatedDir, CurationOptions{
		DatasetType: "chat", GeneratedAt: finishedAt, RightsByID: map[string]RightsGrant{"rights-1": grant},
	})
	require.NoError(t, err)
	require.Equal(t, 1, curated.SampleCount)
	require.Zero(t, curated.ExcludedCount)
	require.NoError(t, VerifyDatasetBundle(curatedDir))
	samples, err := readJSONLines[TrainingSample](filepath.Join(curatedDir, "chat.jsonl"))
	require.NoError(t, err)
	require.Len(t, samples, 1)
	require.Len(t, samples[0].Messages, 2)
	require.Equal(t, "email <EMAIL>", samples[0].Messages[0].Content)
	require.NotContains(t, samples[0].Messages[0].Content, "do not deliver")
	_, err = os.Stat(filepath.Join(curatedDir, "client"))
	require.ErrorIs(t, err, os.ErrNotExist)
	_, err = CreateDeliveryBundle(curatedDir, filepath.Join(root, "unreviewed-delivery"), DeliveryOptions{
		ReleaseID: "release-unreviewed", Recipient: "buyer-a", Contract: "contract-a",
		CreatedAt: finishedAt.Add(time.Hour), RightsByID: map[string]RightsGrant{"rights-1": grant},
	})
	require.ErrorContains(t, err, "reviewed_by and review_reference")

	deliveryDir := filepath.Join(root, "delivery-v1")
	delivery, err := CreateDeliveryBundle(curatedDir, deliveryDir, DeliveryOptions{
		ReleaseID: "release-2026-08-20-a", Recipient: "buyer-a", Contract: "contract-a",
		AllowedPurpose: "model_training", IncludeProvenance: true, CreatedAt: finishedAt.Add(time.Hour),
		ReviewedBy: "reviewer-a", ReviewReference: "review-ticket-1",
		RightsByID: map[string]RightsGrant{"rights-1": grant},
	})
	require.NoError(t, err)
	require.Equal(t, 1, delivery.SourceSampleCount)
	require.Equal(t, 1, delivery.SampleCount)
	require.True(t, delivery.ProvenanceIncluded)
	require.False(t, delivery.RawCaptureIncluded)
	require.NoError(t, VerifyDatasetBundle(deliveryDir))
	deliveryProvenance, err := readJSONLines[ProvenanceRecord](filepath.Join(deliveryDir, "provenance.ndjson"))
	require.NoError(t, err)
	require.Len(t, deliveryProvenance, 1)
	require.Empty(t, deliveryProvenance[0].UserSubjectRef)
	require.Empty(t, deliveryProvenance[0].APIKeySubjectRef)

	apiKeyVeto := RightsGrant{
		RightsID: "rights-key-veto", ScopeType: "api_key", ScopeRef: "key-ref", Version: 1,
		Status: RightsWithdrawn, EffectiveAt: startedAt.Add(-time.Hour),
	}
	_, err = CreateDeliveryBundle(curatedDir, filepath.Join(root, "key-veto-delivery"), DeliveryOptions{
		ReleaseID: "release-key-veto", Recipient: "buyer-a", Contract: "contract-a",
		ReviewedBy: "reviewer-a", ReviewReference: "review-ticket-veto",
		CreatedAt:  finishedAt.Add(90 * time.Minute),
		RightsByID: map[string]RightsGrant{"rights-1": grant, "rights-key-veto": apiKeyVeto},
	})
	require.ErrorContains(t, err, "no samples remain eligible")

	grant.Status = RightsWithdrawn
	_, err = CreateDeliveryBundle(curatedDir, filepath.Join(root, "withdrawn-delivery"), DeliveryOptions{
		ReleaseID: "release-withdrawn", Recipient: "buyer-a", Contract: "contract-a",
		ReviewedBy: "reviewer-a", ReviewReference: "review-ticket-2",
		CreatedAt: finishedAt.Add(2 * time.Hour), RightsByID: map[string]RightsGrant{"rights-1": grant},
	})
	require.ErrorContains(t, err, "no samples remain eligible")
}

func TestVerifyDatasetBundleDetectsTampering(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), []byte("{}\n"), 0o600))
	require.NoError(t, writeSHA256Sums(root, []string{"manifest.json"}))
	require.NoError(t, VerifyDatasetBundle(root))
	require.NoError(t, os.WriteFile(filepath.Join(root, "manifest.json"), []byte("{\"tampered\":true}\n"), 0o600))
	require.ErrorContains(t, VerifyDatasetBundle(root), "checksum mismatch")
}

func TestCurationRejectsCurrentVetoFromTheOtherSubjectScope(t *testing.T) {
	now := time.Now().UTC()
	manifest := Manifest{
		SchemaVersion: "capture-v1", CaptureID: "00000000-0000-0000-0000-000000000010",
		UserSubjectRef: "user-ref", APIKeySubjectRef: "key-ref",
		RightsID: "key-right", RightsVersion: 1, RightsStatus: RightsEligible,
		CaptureComplete: true, ClientResponse: HeaderSnapshot{Status: 200},
		Attempts: []AttemptManifest{{Model: "upstream", HTTPStatus: 200, Complete: true}},
	}
	grants := map[string]RightsGrant{
		"key-right": {
			RightsID: "key-right", ScopeType: "api_key", ScopeRef: "key-ref", Version: 1,
			Status: RightsEligible, AllowedPurposes: []string{"model_training"},
			AllowedDatasetTypes: []string{"chat"}, AllowedRecipients: []string{"buyer-a"},
			EffectiveAt: now.Add(-time.Hour),
		},
		"user-veto": {
			RightsID: "user-veto", ScopeType: "user", ScopeRef: "user-ref", Version: 1,
			Status: RightsWithdrawn, EffectiveAt: now.Add(-time.Hour),
		},
	}
	_, reason := eligibleGrantForArtifact(manifest, grants, "chat", "buyer-a", now)
	require.Equal(t, "current_subject_veto", reason)
}
