package auth

import (
	"strings"
	"testing"
	"time"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}

	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Error("correct password rejected")
	}

	ok, err = VerifyPassword(hash, "correct horse battery stapler")
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Error("wrong password accepted")
	}
}

func TestPasswordHashIsSalted(t *testing.T) {
	a, err := HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Error("identical passwords produced identical hashes; salt is not applied")
	}
}

func TestPasswordHashEncodesParameters(t *testing.T) {
	hash, err := HashPassword("x")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
		t.Errorf("unexpected hash prefix: %s", hash)
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	for _, bad := range []string{
		"",
		"plaintext",
		"$argon2id$v=19$m=65536,t=3,p=4$onlyonefield",
		"$bcrypt$v=19$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=1$m=65536,t=3,p=4$c2FsdA$aGFzaA",
		"$argon2id$v=19$garbage$c2FsdA$aGFzaA",
		"$argon2id$v=19$m=65536,t=3,p=4$!!!$aGFzaA",
	} {
		t.Run(bad, func(t *testing.T) {
			ok, err := VerifyPassword(bad, "anything")
			if ok {
				t.Error("malformed hash verified successfully")
			}
			if err == nil {
				t.Error("malformed hash produced no error")
			}
		})
	}
}

func TestTokensAreUniqueAndHashed(t *testing.T) {
	token, hash, err := NewToken()
	if err != nil {
		t.Fatal(err)
	}
	if token == "" || hash == "" {
		t.Fatal("empty token or hash")
	}
	if token == hash {
		t.Error("token stored verbatim rather than hashed")
	}
	if HashToken(token) != hash {
		t.Error("HashToken does not reproduce the stored hash")
	}
	if len(hash) != 64 {
		t.Errorf("hash length = %d, want 64 hex chars", len(hash))
	}

	seen := map[string]bool{}
	for range 100 {
		tok, _, err := NewToken()
		if err != nil {
			t.Fatal(err)
		}
		if seen[tok] {
			t.Fatal("NewToken returned a duplicate")
		}
		seen[tok] = true
	}
}

func TestLimiterBlocksAfterMax(t *testing.T) {
	clock := time.Now()
	l := NewLimiter(3, time.Minute)
	l.now = func() time.Time { return clock }

	for i := range 3 {
		if ok, _ := l.Allow("k"); !ok {
			t.Fatalf("attempt %d blocked early", i+1)
		}
	}

	ok, retryAfter := l.Allow("k")
	if ok {
		t.Fatal("fourth attempt was allowed")
	}
	if retryAfter <= 0 || retryAfter > time.Minute {
		t.Errorf("retryAfter = %v, want within the window", retryAfter)
	}
}

func TestLimiterWindowExpires(t *testing.T) {
	clock := time.Now()
	l := NewLimiter(1, time.Minute)
	l.now = func() time.Time { return clock }

	l.Allow("k")
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("second attempt allowed inside the window")
	}

	clock = clock.Add(time.Minute + time.Second)
	if ok, _ := l.Allow("k"); !ok {
		t.Error("attempt blocked after the window expired")
	}
}

func TestLimiterResetClearsAttempts(t *testing.T) {
	l := NewLimiter(1, time.Minute)
	l.Allow("k")
	if ok, _ := l.Allow("k"); ok {
		t.Fatal("limiter did not engage")
	}

	l.Reset("k")
	if ok, _ := l.Allow("k"); !ok {
		t.Error("Reset did not clear the key")
	}
}

func TestLimiterKeysAreIndependent(t *testing.T) {
	l := NewLimiter(1, time.Minute)
	l.Allow("a")
	if ok, _ := l.Allow("b"); !ok {
		t.Error("one key's attempts throttled another")
	}
}

// A shared NAT address must not let one client's failures lock out the rest,
// which is why the address key is paired with a username key rather than
// being the only guard.
func TestLimiterSweepDropsExpiredKeys(t *testing.T) {
	clock := time.Now()
	l := NewLimiter(1, time.Minute)
	l.now = func() time.Time { return clock }

	l.Allow("old")
	clock = clock.Add(2 * time.Minute)
	l.Allow("new")

	l.mu.Lock()
	_, stillThere := l.buckets["old"]
	l.mu.Unlock()
	if stillThere {
		t.Error("expired bucket was not swept")
	}
}
