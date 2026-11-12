package model

import (
	"testing"
	"time"
)

func date(s string) *time.Time {
	t, err := time.Parse(DateLayout, s)
	if err != nil {
		panic(err)
	}
	return &t
}

func TestAgeOn(t *testing.T) {
	tests := []struct {
		name  string
		birth string
		on    string
		want  int
	}{
		{"the day before a birthday", "2015-06-15", "2026-06-14", 10},
		{"on the birthday", "2015-06-15", "2026-06-15", 11},
		{"the day after", "2015-06-15", "2026-06-16", 11},
		{"born in december, checked in january", "2015-12-31", "2026-01-01", 10},
		{"an adult", "1986-03-02", "2026-11-12", 40},
		{"born today", "2026-11-12", "2026-11-12", 0},
		{"a future birth date clamps to zero", "2030-01-01", "2026-11-12", 0},

		{"leap-year birth, birthday in a common year", "2016-03-01", "2026-03-01", 10},
		{"leap-year birth, day before", "2016-03-01", "2026-02-28", 9},
		{"february 29 birthday rolls over on march 1", "2016-02-29", "2026-03-01", 10},
		{"february 29 birthday not yet on february 28", "2016-02-29", "2026-02-28", 9},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			u := &User{BirthDate: date(tc.birth)}
			on, err := time.Parse(DateLayout, tc.on)
			if err != nil {
				t.Fatal(err)
			}
			if got := u.AgeOn(on); got != tc.want {
				t.Errorf("AgeOn(%s) = %d, want %d", tc.on, got, tc.want)
			}
		})
	}
}

func TestAgeUnknownWithoutBirthDate(t *testing.T) {
	u := &User{}
	if _, ok := u.Age(); ok {
		t.Error("Age reported a value with no birth date set")
	}
}

func TestHasPIN(t *testing.T) {
	if (&User{}).HasPIN() {
		t.Error("an empty PIN hash reported as having a PIN")
	}
	if !(&User{PINHash: "$argon2id$..."}).HasPIN() {
		t.Error("a set PIN hash reported as having none")
	}
}
