package siteops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/bejix/upstream-ops/backend/config"
	"github.com/bejix/upstream-ops/backend/crypto"
	"github.com/bejix/upstream-ops/backend/storage"
)

type audienceFixture struct {
	MailAudienceClient
	pages [][]userWire
	subs  []MailDirectorySubscription
	total *int64
	calls int
}

func (f *audienceFixture) BudgetHeadroomForAnalytics(uint, int) (int, int, int) { return 100, 0, 0 }
func (f *audienceFixture) FetchMailUsersPage(ctx context.Context, inst TargetEndpoint, n int, project MailProjectAddress) (MailDirectoryPage[MailDirectoryUser], UpstreamFetchMeta, error) {
	f.calls++
	if n > len(f.pages) {
		return MailDirectoryPage[MailDirectoryUser]{}, UpstreamFetchMeta{}, errors.New("unexpected extra page")
	}
	page := MailDirectoryPage[MailDirectoryUser]{Page: ptr(n), PageSize: ptr(len(f.pages[0])), HasMore: ptr(n < len(f.pages)), Total: f.total}
	for _, w := range f.pages[n-1] {
		u := MailDirectoryUser{ID: w.ID, Role: w.Role, Status: w.Status, CreatedAt: w.CreatedAt, BalanceUSD: w.Balance}
		if u.Status == "active" && u.Role == "user" {
			p, e := project(u, w.Email)
			if e != nil {
				return page, UpstreamFetchMeta{}, e
			}
			u.Address = p
		}
		page.Items = append(page.Items, u)
	}
	return page, UpstreamFetchMeta{}, nil
}
func (f *audienceFixture) FetchMailSubscriptionsPage(context.Context, TargetEndpoint, int) (MailDirectoryPage[MailDirectorySubscription], UpstreamFetchMeta, error) {
	return MailDirectoryPage[MailDirectorySubscription]{Items: f.subs, HasMore: ptr(false)}, UpstreamFetchMeta{}, nil
}
func (f *audienceFixture) FetchUsersUsageBatch(context.Context, TargetEndpoint, []int64) (map[int64]SanitizedUserQuickUsage, UpstreamFetchMeta, error) {
	return nil, UpstreamFetchMeta{}, nil
}
func audienceSetup(t *testing.T) (*audienceFixture, MailAudienceOptions) {
	now := time.Now()
	from, until := now.Add(-time.Hour), now.Add(time.Hour)
	c, _ := crypto.NewCipher(t.Name())
	u := func(id int64, email string) userWire {
		return userWire{ID: id, Email: email, Role: "user", Status: "active", CreatedAt: &from, Balance: ptr(25.0)}
	}
	f := &audienceFixture{pages: [][]userWire{{u(5, "first@example.com"), u(2, "second@example.com")}, {u(1, "FIRST@example.com"), u(4, "fourth@example.com")}}, subs: []MailDirectorySubscription{{ID: 1, UserID: 2, GroupID: 8, GroupName: "A", Status: "active", StartsAt: &from, ExpiresAt: &until}}}
	o := MailAudienceOptions{AsOf: now, Spec: MailAudienceSpec{Mode: MailAudienceModeAll}, Scope: storage.MailScopeKey{InstanceID: 1, TargetBinding: "A"}, Seed: make([]byte, 32), Cipher: c, FindSuppressed: func(context.Context, storage.MailScopeKey, []string) (map[string]bool, error) { return nil, nil }}
	return f, o
}
func TestMailAudienceModes(t *testing.T) {
	f, o := audienceSetup(t)
	hash, _ := MailEmailHash(o.Seed, "fourth@example.com")
	o.FindSuppressed = func(context.Context, storage.MailScopeKey, []string) (map[string]bool, error) {
		return map[string]bool{hash: true}, nil
	}
	r, e := ResolveMailAudience(context.Background(), f, o)
	if e != nil || len(r.Rows) != 0 || r.Preview.RecipientCount != 2 || r.Preview.DuplicateCount != 1 || r.Preview.SuppressedCount != 1 || r.Preview.Samples[0].UserID != 1 {
		t.Fatal(r, e)
	}
	o.Spec = MailAudienceSpec{Mode: MailAudienceModeSelected, UserIDs: []int64{5, 2, 2}}
	f.calls = 0
	r, e = ResolveMailAudience(context.Background(), f, o)
	if e != nil || f.calls != 1 || r.Preview.RecipientCount != 2 {
		t.Fatal("selected early stop", r, e)
	}
	o.Spec = MailAudienceSpec{Mode: MailAudienceModeGroups, GroupIDs: []int64{8}}
	o.BatchID = 7
	o.UsedVars = []MailTemplateVar{MailTemplateVarPlanName}
	r, e = ResolveMailAudience(context.Background(), f, o)
	if e != nil || len(r.Rows) != 1 || r.Rows[0].UserID != 2 {
		t.Fatal("groups", r, e)
	}
	row := r.Rows[0]
	payload, e := OpenMailItem(o.Cipher, o.Scope, o.Seed, storage.SiteOpsMailItem{BatchID: 7, UserID: row.UserID, EmailHash: row.EmailHash, PayloadCipher: &row.PayloadCipher})
	if e != nil || payload.Vars.PlanName != "A" || payload.Vars.BalanceUSD != "" {
		t.Fatal("personalized projection", e)
	}
	o.Spec = MailAudienceSpec{Mode: MailAudienceModeSegment, Tags: []MailSegmentTag{MailSegmentTagExpiringSoon}}
	r, e = ResolveMailAudience(context.Background(), f, o)
	if e != nil || len(r.Rows) != 1 || r.Rows[0].UserID != 2 {
		t.Fatal("segment", r, e)
	}
	o.Spec = MailAudienceSpec{Mode: MailAudienceMode(MailBatchModeRetryFailed)}
	o.RetryFailed = map[int64]string{2: row.EmailHash, 5: row.EmailHash}
	r, e = ResolveMailAudience(context.Background(), f, o)
	if e != nil || len(r.Rows) != 1 || r.Rows[0].UserID != 2 || r.Counts.Excluded != 1 {
		t.Fatal("retry UID/hash mismatch", r, e)
	}
}
func TestMailAudienceCoverageAndDrift(t *testing.T) {
	f, o := audienceSetup(t)
	o.Spec = MailAudienceSpec{Mode: MailAudienceModeSegment, ActivityStates: []MailActivityState{MailActivityStateLow}}
	if _, e := ResolveMailAudience(context.Background(), f, o); e != ErrMailPrepCoverageIncomplete {
		t.Fatal("missing usage became low", e)
	}
	o.Spec = MailAudienceSpec{Mode: MailAudienceModeAll}
	f.pages[1][0].ID = f.pages[0][0].ID
	if _, e := ResolveMailAudience(context.Background(), f, o); e != ErrMailPrepPageDrift {
		t.Fatal("duplicate ID", e)
	}
	f.total = ptr(int64(0))
	if _, e := ResolveMailAudience(context.Background(), f, o); e != ErrMailPrepPageDrift {
		t.Fatal("inconsistent total", e)
	}
	f.total = nil
	f.pages = nil
	for i := int64(1); i <= 6; i++ {
		f.pages = append(f.pages, []userWire{{ID: i, Role: "user", Status: "active", Email: fmt.Sprintf("user%d@example.com", i)}})
	}
	r, e := ResolveMailAudience(context.Background(), f, o)
	if e != nil || !r.Preview.IsEstimate || r.Counts.Pages != 5 || r.Preview.RecipientCount != 5 {
		t.Fatal("preview budget", r, e)
	}
}
func TestMailPrivateClientIsolation(t *testing.T) {
	var hits atomic.Int32
	var backoff atomic.Bool
	start := make(chan struct{}, 2)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if r.Header.Get("If-None-Match") != "" {
			t.Error("private request used ETag")
		}
		if backoff.Load() {
			w.WriteHeader(429)
			return
		}
		start <- struct{}{}
		<-release
		w.Header().Set("ETag", "private-tag")
		fmt.Fprint(w, `{"items":[{"id":4,"email":"private@example.com","role":"user","status":"active","secret":"ignored"}]}`)
	}))
	defer server.Close()
	defer unblock()
	c, inst := testClient(server.URL, config.SiteOpsConfig{PerInstanceMaxConcurrency: 4})
	out := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func(i int) {
			project := func(u MailDirectoryUser, email string) (MailAddressProjection, error) {
				return MailAddressProjection{EmailHash: fmt.Sprint(i)}, nil
			}
			p, meta, e := c.FetchMailUsersPage(context.Background(), inst, 1, project)
			if e == nil {
				encoded, _ := json.Marshal(p)
				if len(p.Items) != 1 || p.Items[0].Address.EmailHash != fmt.Sprint(i) || p.Page != nil || p.PageSize != nil || p.Total != nil || p.HasMore != nil || meta.ETag != "" || strings.Contains(string(encoded), "private@example.com") {
					e = errors.New("projection or metadata leaked")
				}
			}
			out <- e
		}(i)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-start:
		case <-time.After(2 * time.Second):
			t.Fatal("private requests shared a flight")
		}
	}
	c.mu.Lock()
	flights := len(c.flights)
	c.mu.Unlock()
	if flights != 0 {
		t.Fatal("private request in shared flight map")
	}
	unblock()
	for i := 0; i < 2; i++ {
		if err := <-out; err != nil {
			t.Fatal(err)
		}
	}
	if len(c.cache) != 0 || hits.Load() != 2 {
		t.Fatal("unexpected shared state")
	}
	backoff.Store(true)
	_, _, e := c.FetchMailSubscriptionsPage(context.Background(), inst, 1)
	if !errors.Is(e, ErrRateLimited) {
		t.Fatal(e)
	}
	_, _, e = c.FetchMailUsersPage(context.Background(), inst, 1, nil)
	if !errors.Is(e, ErrRateLimited) || hits.Load() != 3 {
		t.Fatal("backoff not shared", e)
	}
	other, endpoint := testClient(server.URL, config.SiteOpsConfig{PerInstanceMaxConcurrency: 1})
	if _, _, e := other.FetchMailUsersPage(context.Background(), endpoint, 1, nil); !errors.Is(e, ErrBudgetExceeded) {
		t.Fatal("missing L1 slot reserve", e)
	}
}
