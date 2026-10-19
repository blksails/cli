package auth

import (
	"errors"
	"os"
	"testing"
	"time"

	"github.com/supabase-community/gotrue-go"
)

// Run with BK_AUTH_SIMULATION_URL=http://127.0.0.1:4919/auth and the
// checked-in Postman simulation. Calls the production GoTrue SDK over HTTP.
func TestRefreshAgainstSimulation(t *testing.T) {
	endpoint := os.Getenv("BK_AUTH_SIMULATION_URL")
	if endpoint == "" {
		t.Skip("start postman/simulations/bk-auth.sim.yaml to run HTTP simulation")
	}
	ref := GoTrueRefresher{Client: gotrue.New("test", "test-anon").WithCustomGoTrueURL(endpoint)}
	for _, tc := range []struct {
		token string
		want  error
	}{{"valid", nil}, {"outage", ErrRefreshUnavailable}, {"revoked", ErrReloginRequired}} {
		t.Run(tc.token, func(t *testing.T) {
			now := time.Now()
			s := expiredSession(now)
			s.RefreshToken = tc.token
			path := writeAuthFile(t, "default", s)
			before, _ := os.ReadFile(path)
			got, err := EnsureFresh(path, "default", now, testSkew, ref)
			if tc.want == nil {
				if err != nil || got.RefreshToken != "test-rotated" {
					t.Fatalf("refresh failed: %v", err)
				}
			} else {
				if !errors.Is(err, tc.want) {
					t.Fatalf("wanted %v got %v", tc.want, err)
				}
				after, _ := os.ReadFile(path)
				if string(before) != string(after) {
					t.Fatal("failure modified credentials")
				}
			}
		})
	}
}
