package server

import (
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/flinternet/flinternet/internal/pinterest"
)

func TestShareCodecSealOpenAndTamperResistance(t *testing.T) {
	nilCodec, err := newShareCodec("   ")
	if err != nil || nilCodec != nil {
		t.Fatalf("expected blank share key to disable codec, got codec=%v err=%v", nilCodec, err)
	}

	codec, err := newShareCodec("correct-horse-battery-staple")
	if err != nil || codec == nil {
		t.Fatalf("newShareCodec failed: %v", err)
	}

	src := pinSource{
		Kind:     sourceSearch,
		Target:   "secret-search-query",
		Sub:      "pins",
		Bookmark: "cursor-page-2",
		AggID:    "5172215728905962662",
		MediaKey: "8db4689511ade5e108bc8153f577a960",
	}
	tok, err := codec.seal("e1_pin_alpha", src)
	if err != nil || tok == "" {
		t.Fatalf("seal failed: err=%v tok=%q", err, tok)
	}
	if strings.Contains(tok, "secret-search-query") || strings.Contains(tok, "cursor-page-2") {
		t.Fatalf("plaintext history leaked inside sealed token: %q", tok)
	}

	got, ok := codec.open("e1_pin_alpha", tok)
	if !ok || got != src {
		t.Fatalf("open mismatch: ok=%v got=%+v want=%+v", ok, got, src)
	}

	// Cross-pin transplant must fail AAD check.
	if _, ok := codec.open("e1_pin_other", tok); ok {
		t.Fatalf("expected token bound to e1_pin_alpha to fail verification for e1_pin_other")
	}

	// Tampered ciphertext must fail verification.
	badTok := tok[:len(tok)-1] + "A"
	if badTok == tok {
		badTok = tok[:len(tok)-1] + "B"
	}
	if _, ok := codec.open("e1_pin_alpha", badTok); ok {
		t.Fatalf("expected tampered token to be rejected")
	}

	// Different server key must reject the token.
	otherCodec, _ := newShareCodec("different-key")
	if _, ok := otherCodec.open("e1_pin_alpha", tok); ok {
		t.Fatalf("expected wrong key to reject token")
	}
}

func TestExtractMediaKeyAndMatchCorrelatedPin(t *testing.T) {
	if got := extractMediaKey("https://i.pinimg.com/originals/8d/b4/68/8db4689511ade5e108bc8153f577a960.jpg?x=1"); got != "8db4689511ade5e108bc8153f577a960" {
		t.Fatalf("unexpected image media key: %q", got)
	}
	if got := extractMediaKey("https://v1.pinimg.com/videos/v2/hls/d8/2b/dd/d82bdda6d07a8ff6ff66194674f5093a_mobile.m3u8"); got != "d82bdda6d07a8ff6ff66194674f5093a" {
		t.Fatalf("unexpected video media key: %q", got)
	}

	pins := []pinterest.Pin{
		{ID: "e1_rotated_1", AggregatedPinID: "111", FallbackImageURL: "https://i.pinimg.com/736x/aa/bb/cc/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa.jpg"},
		{ID: "e1_rotated_2", AggregatedPinID: "222", FallbackImageURL: "https://i.pinimg.com/736x/dd/ee/ff/bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb.jpg"},
	}

	// Rotated e1_ ID resolved unambiguously via AggID + MediaKey.
	matched, ok := matchCorrelatedPin(pins, "e1_old_token", pinSource{
		Kind:     sourceBoard,
		Target:   "brd-1",
		AggID:    "222",
		MediaKey: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
	})
	if !ok || matched.ID != "e1_rotated_2" {
		t.Fatalf("expected fingerprint match for rotated ID, got ok=%v matched=%+v", ok, matched)
	}
}

func TestSingleflightDeduplicatesConcurrentCalls(t *testing.T) {
	var grp singleflightGroup[string]
	var calls atomic.Int32
	firstEntered := make(chan struct{})
	release := make(chan struct{})

	var wg sync.WaitGroup
	results := make([]string, 8)
	wg.Add(1)
	go func() {
		defer wg.Done()
		v, err := grp.do("same-key", func() (string, error) {
			calls.Add(1)
			close(firstEntered)
			<-release
			return "resolved", nil
		})
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		results[0] = v
	}()

	<-firstEntered
	for i := 1; i < len(results); i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			v, err := grp.do("same-key", func() (string, error) {
				calls.Add(1)
				return "resolved", nil
			})
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			results[idx] = v
		}(i)
	}

	time.Sleep(15 * time.Millisecond)
	close(release)
	wg.Wait()

	if calls.Load() != 1 {
		t.Fatalf("expected singleflight to execute fn exactly once, got %d", calls.Load())
	}
	for i, r := range results {
		if r != "resolved" {
			t.Fatalf("worker %d got %q", i, r)
		}
	}

	if _, err := grp.do("err-key", func() (string, error) { return "", errors.New("boom") }); err == nil {
		t.Fatalf("expected error to propagate")
	}
}
