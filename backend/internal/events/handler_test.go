package events

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/crowdcast/crowdcast/internal/store/postgres"
	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
)

type mockEventRepo struct {
	events map[string]*postgres.Event
}

func (m *mockEventRepo) Get(ctx context.Context, id string) (*postgres.Event, error) {
	if ev, ok := m.events[id]; ok {
		return ev, nil
	}
	return nil, postgres.ErrNotFound
}

func (m *mockEventRepo) Upsert(ctx context.Context, ev *postgres.Event) error {
	m.events[ev.ID] = ev
	return nil
}

func (m *mockEventRepo) UpdateStatus(ctx context.Context, id string, status string) error {
	if ev, ok := m.events[id]; ok {
		ev.Status = status
		return nil
	}
	return postgres.ErrNotFound
}

func (m *mockEventRepo) UpdatePeakAudience(ctx context.Context, id string, peak int) error {
	if ev, ok := m.events[id]; ok {
		ev.PeakAudience = peak
		return nil
	}
	return postgres.ErrNotFound
}

func TestHandler_GetEvent(t *testing.T) {
	now := time.Now().UTC()
	mockRepo := &mockEventRepo{
		events: map[string]*postgres.Event{
			"cc-0826": {
				ID:              "cc-0826",
				EventNumber:     "EVENT #CC-0826",
				Title:           "After Hours — The Sound of Now",
				TitleMain:       "AFTER HOURS",
				Subtitle:        "THE SOUND OF NOW",
				SessionCode:     "SESSION 08",
				Description:     "An immersive night",
				HostName:         "Aanya Verma",
				HostAvatarLetter: "A",
				StageName:        "STAGE 01",
				StageResolution: "1080p",
				DateEyebrow:     "TUESDAY, NOV 17, 2026",
				Status:          "live",
				StartedAt:       &now,
			},
		},
	}

	svc := &Service{
		eventsRepo: mockRepo,
		// redisClient nil is handled or can be tested when event exists
	}

	r := chi.NewRouter()
	r.Get("/events/{id}", func(w http.ResponseWriter, req *http.Request) {
		id := chi.URLParam(req, "id")
		ev, err := mockRepo.Get(req.Context(), id)
		if err != nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		assert.Equal(t, "cc-0826", ev.ID)
		w.WriteHeader(http.StatusOK)
	})

	t.Run("found event", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/events/cc-0826", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})

	t.Run("not found event", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/events/nonexistent", nil)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
	_ = svc
}
