package predictionstore

import (
	"errors"
	"strings"
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func mustCreate(t *testing.T, s *Store, p *Prediction) *Prediction {
	t.Helper()
	created, err := s.Create(p)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return created
}

func samplePrediction() *Prediction {
	return &Prediction{
		Claim:              "PATCH with an unknown field returns 400",
		ResolutionCriteria: "curl a PATCH with a misspelled key against a throwaway job; read the status",
		Category:           "behavior",
		Provenance:         "inferred",
		Author:             "claude-code",
		Probability:        0.85,
		Tags:               []string{"scheduler", "api"},
	}
}

func TestCreateAssignsAPrefixedID(t *testing.T) {
	s := newTestStore(t)
	first := mustCreate(t, s, samplePrediction())
	if first.ID != "prediction_000001" {
		t.Fatalf("first id = %q, want prediction_000001", first.ID)
	}
	second := mustCreate(t, s, samplePrediction())
	if second.ID != "prediction_000002" {
		t.Fatalf("second id = %q, want prediction_000002", second.ID)
	}
	// The prefix is what keeps this store out of every bare-uuid resolution in
	// chat, and keeps '/' out of a ref kanban-store splits on.
	if strings.Contains(first.ID, "/") {
		t.Fatalf("id %q contains a slash", first.ID)
	}
}

func TestResolutionCriteriaIsRequired(t *testing.T) {
	s := newTestStore(t)
	p := samplePrediction()
	p.ResolutionCriteria = "   "
	_, err := s.Create(p)
	if !errors.Is(err, ErrInvalidPrediction) {
		t.Fatalf("err = %v, want ErrInvalidPrediction", err)
	}
	if !strings.Contains(err.Error(), "OF WHAT") {
		t.Fatalf("error should say what is missing and why, got: %v", err)
	}
}

func TestOffLadderProbabilityIsRefusedAndNamesTheRungs(t *testing.T) {
	s := newTestStore(t)
	p := samplePrediction()
	p.Probability = 0.73
	_, err := s.Create(p)
	if !errors.Is(err, ErrInvalidPrediction) {
		t.Fatalf("err = %v, want ErrInvalidPrediction", err)
	}
	if !strings.Contains(err.Error(), "0.7") || !strings.Contains(err.Error(), "0.75") {
		t.Fatalf("error should name the two nearest rungs, got: %v", err)
	}
}

func TestCertaintyIsNotAProbability(t *testing.T) {
	s := newTestStore(t)
	for _, p := range []float64{0, 1, -0.5, 1.5} {
		input := samplePrediction()
		input.Probability = p
		if _, err := s.Create(input); !errors.Is(err, ErrInvalidPrediction) {
			t.Fatalf("probability %v was accepted", p)
		}
	}
}

func TestUnknownCategoryNamesTheVocabulary(t *testing.T) {
	s := newTestStore(t)
	p := samplePrediction()
	p.Category = "vibes"
	_, err := s.Create(p)
	if !errors.Is(err, ErrInvalidPrediction) {
		t.Fatalf("err = %v, want ErrInvalidPrediction", err)
	}
	for _, c := range Categories {
		if !strings.Contains(err.Error(), c) {
			t.Fatalf("error should list %q, got: %v", c, err)
		}
	}
}

func TestCategorySynonymsResolve(t *testing.T) {
	s := newTestStore(t)
	p := samplePrediction()
	p.Category = "Diagnosis"
	created := mustCreate(t, s, p)
	if created.Category != "root-cause" {
		t.Fatalf("category = %q, want root-cause", created.Category)
	}
}

func TestUnknownProvenanceIsRefused(t *testing.T) {
	s := newTestStore(t)
	p := samplePrediction()
	p.Provenance = "hunch"
	if _, err := s.Create(p); !errors.Is(err, ErrInvalidPrediction) {
		t.Fatalf("err = %v, want ErrInvalidPrediction", err)
	}
}

func TestTheOpeningOddsAreThemselvesAnEstimate(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	if len(created.Estimates) != 1 {
		t.Fatalf("estimates = %d, want 1 — the trail should start full", len(created.Estimates))
	}
	if created.Estimates[0].Probability != 0.85 {
		t.Fatalf("opening estimate = %v, want 0.85", created.Estimates[0].Probability)
	}
}

func TestRevisingKeepsTheOldNumberAndFreezesTheFirst(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	updated, err := s.AddEstimate(created.ID, &Estimate{
		Probability: 0.3,
		Rationale:   "read the row back; the field was dropped",
		Provenance:  "measured",
	})
	if err != nil {
		t.Fatalf("add estimate: %v", err)
	}
	if updated.Probability != 0.3 {
		t.Fatalf("current probability = %v, want 0.3", updated.Probability)
	}
	if updated.InitialProbability != 0.85 {
		t.Fatalf("initial probability = %v, want 0.85 — the opening number must not move", updated.InitialProbability)
	}
	if len(updated.Estimates) != 2 {
		t.Fatalf("estimates = %d, want 2 — the old number must survive", len(updated.Estimates))
	}
	if updated.Estimates[0].Probability != 0.85 {
		t.Fatalf("first estimate = %v, want 0.85", updated.Estimates[0].Probability)
	}
	if updated.Provenance != "measured" {
		t.Fatalf("provenance = %q, want measured — a revision that measured something upgrades the provenance", updated.Provenance)
	}
}

func TestRevisingAfterTheAnswerIsKnownIsRefused(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	if _, err := s.Resolve(created.ID, "true", "measured live"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	_, err := s.AddEstimate(created.ID, &Estimate{Probability: 0.99, Provenance: "measured"})
	if !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("err = %v, want ErrAlreadyResolved", err)
	}
}

func TestResolvingTwiceIsRefused(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	if _, err := s.Resolve(created.ID, "true", ""); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if _, err := s.Resolve(created.ID, "false", ""); !errors.Is(err, ErrAlreadyResolved) {
		t.Fatalf("err = %v, want ErrAlreadyResolved", err)
	}
}

func TestVoidIsNotAMiss(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	voided, err := s.Resolve(created.ID, "void", "the work was cancelled")
	if err != nil {
		t.Fatalf("resolve void: %v", err)
	}
	if voided.Status != "void" || voided.Outcome != "" {
		t.Fatalf("status/outcome = %q/%q, want void/empty", voided.Status, voided.Outcome)
	}
	report, err := s.Calibration(Filter{}, "")
	if err != nil {
		t.Fatalf("calibration: %v", err)
	}
	if report.Overall.N != 0 {
		t.Fatalf("scored %d predictions, want 0 — a void claim is not a wrong one", report.Overall.N)
	}
	if report.Overall.Void != 1 {
		t.Fatalf("void count = %d, want 1", report.Overall.Void)
	}
}

func TestPatchCannotMoveTheNumberOrTheOutcome(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	newClaim := "PATCH with an unknown field returns 400, measured on a throwaway job"
	updated, err := s.Patch(created.ID, Patch{Claim: &newClaim})
	if err != nil {
		t.Fatalf("patch: %v", err)
	}
	if updated.Claim != newClaim {
		t.Fatalf("claim did not change")
	}
	if updated.Probability != 0.85 || updated.Status != "open" {
		t.Fatalf("patch moved the number or the status: %v %q", updated.Probability, updated.Status)
	}
	// Patch has nowhere to put a probability or an outcome; the HTTP layer
	// turns an attempt into a 400 naming the right route (see server_test.go).
}

func TestTagFilterMatchesWholeTagsOnly(t *testing.T) {
	s := newTestStore(t)
	p := samplePrediction()
	p.Tags = []string{"scheduler-api"}
	mustCreate(t, s, p)
	got, err := s.List(Filter{Tag: "scheduler"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("tag %q matched %q", "scheduler", "scheduler-api")
	}
	got, err = s.List(Filter{Tag: "scheduler-api"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("whole-tag match returned %d rows, want 1", len(got))
	}
}

func TestSearchFindsClaimAndCriteria(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, samplePrediction())
	got, err := s.List(Filter{Query: "throwaway"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("search over resolution_criteria returned %d rows, want 1", len(got))
	}
}

func TestBadSearchQueryIsTheCallersFault(t *testing.T) {
	s := newTestStore(t)
	mustCreate(t, s, samplePrediction())
	if _, err := s.List(Filter{Query: `"unclosed`}); !errors.Is(err, ErrInvalidPrediction) {
		t.Fatalf("err = %v, want ErrInvalidPrediction", err)
	}
}

func TestSoftDeleteHidesAndRestoreBringsBack(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	if err := s.SoftDelete(created.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	got, _ := s.List(Filter{})
	if len(got) != 0 {
		t.Fatalf("deleted prediction still listed")
	}
	if _, err := s.Restore(created.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	got, _ = s.List(Filter{})
	if len(got) != 1 {
		t.Fatalf("restore did not bring it back")
	}
}

func TestLinkingIsIdempotent(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	link := &Link{EntityType: "note", EntityRef: "f1db12e8-6857-46d9-80ae-9bc4e0d7c200", Label: "the todo"}
	if _, err := s.AddLink(created.ID, link); err != nil {
		t.Fatalf("add link: %v", err)
	}
	if _, err := s.AddLink(created.ID, link); err != nil {
		t.Fatalf("re-link: %v", err)
	}
	links, err := s.ListLinks(created.ID)
	if err != nil {
		t.Fatalf("list links: %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("links = %d, want 1 — re-linking must be idempotent", len(links))
	}
}

func TestLinkNeedsBothTypeAndRef(t *testing.T) {
	s := newTestStore(t)
	created := mustCreate(t, s, samplePrediction())
	if _, err := s.AddLink(created.ID, &Link{EntityType: "note"}); !errors.Is(err, ErrInvalidLink) {
		t.Fatalf("err = %v, want ErrInvalidLink", err)
	}
}

func TestOverdueFindsOnlyUnresolvedPastDeadlines(t *testing.T) {
	s := newTestStore(t)
	past := samplePrediction()
	past.DueAt = 1
	overdue := mustCreate(t, s, past)

	future := samplePrediction()
	future.DueAt = now() + 86400
	mustCreate(t, s, future)

	noDeadline := samplePrediction()
	mustCreate(t, s, noDeadline)

	got, err := s.List(Filter{Overdue: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].ID != overdue.ID {
		t.Fatalf("overdue returned %d rows, want just %s", len(got), overdue.ID)
	}
	if _, err := s.Resolve(overdue.ID, "true", ""); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	got, _ = s.List(Filter{Overdue: true})
	if len(got) != 0 {
		t.Fatalf("a resolved prediction is still reported overdue")
	}
}
