package webpush

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestEncryptRFC8291 checks the example of RFC 8291, appendix A.
func TestEncryptRFC8291(t *testing.T) {
	d := func(s string) []byte {
		b, err := unb64(s)
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	as, err := ecdh.P256().NewPrivateKey(d("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw"))
	if err != nil {
		t.Fatal(err)
	}
	ua, err := ecdh.P256().NewPublicKey(d("BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := encrypt(as, ua, d("BTBZMqHH6r4Tts7J_aSIgg"), d("DGv6ra1nlYgDCS1FRnbzlw"), []byte("When I grow up, I want to be a watermelon"))
	if err != nil {
		t.Fatal(err)
	}
	want := "DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A_yl95bQpu6cVPTpK4Mqgkf1CXztLVBSt2Ks3oZwbuwXPXLWyouBWLVWGNWQexSgSxsj_Qulcy4a-fN"
	if b64(got) != want {
		t.Errorf("encrypted =\n%s\nwant\n%s", b64(got), want)
	}
}

// decrypt is what the browser does with a message.
func decrypt(t *testing.T, ua *ecdh.PrivateKey, authSecret, body []byte) []byte {
	t.Helper()
	salt, idLen := body[:16], int(body[20])
	as, err := ecdh.P256().NewPublicKey(body[21 : 21+idLen])
	if err != nil {
		t.Fatal(err)
	}
	secret, _ := ua.ECDH(as)
	prkKey, _ := hkdf.Extract(sha256.New, secret, authSecret)
	ikm, _ := hkdf.Expand(sha256.New, prkKey, "WebPush: info\x00"+string(ua.PublicKey().Bytes())+string(as.Bytes()), 32)
	prk, _ := hkdf.Extract(sha256.New, ikm, salt)
	cek, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: aes128gcm\x00", 16)
	nonce, _ := hkdf.Expand(sha256.New, prk, "Content-Encoding: nonce\x00", 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	plain, err := gcm.Open(nil, nonce, body[21+idLen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	if plain[len(plain)-1] != 2 {
		t.Fatalf("missing record delimiter")
	}
	return plain[:len(plain)-1]
}

func TestSend(t *testing.T) {
	keys, err := GenerateKeys()
	if err != nil {
		t.Fatal(err)
	}
	sender, err := NewSender(keys, "mailto:admin@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	if sender.PublicKey() != keys.Public {
		t.Errorf("public key %q, want %q", sender.PublicKey(), keys.Public)
	}
	browser, _ := ecdh.P256().GenerateKey(rand.Reader)
	auth := make([]byte, 16)
	_, _ = rand.Read(auth)

	var got []byte
	status := http.StatusCreated
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Content-Encoding") != "aes128gcm" || r.Header.Get("TTL") != "3600" {
			t.Errorf("headers: %v", r.Header)
		}
		checkVAPID(t, r.Header.Get("Authorization"), keys.Public, "https://"+r.Host)
		body := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(body)
		got = decrypt(t, browser, auth, body)
		w.WriteHeader(status)
	}))
	defer srv.Close()
	sender.client = srv.Client()

	sub := Subscription{Endpoint: srv.URL + "/push/abc", P256dh: b64(browser.PublicKey().Bytes()), Auth: base64.URLEncoding.EncodeToString(auth)}
	if err := sender.Send(context.Background(), sub, []byte(`{"title":"Hi"}`), time.Hour); err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"title":"Hi"}` {
		t.Errorf("browser got %q", got)
	}
	status = http.StatusGone
	if err := sender.Send(context.Background(), sub, []byte("x"), time.Hour); !errors.Is(err, ErrGone) {
		t.Errorf("gone subscription: %v", err)
	}
	if err := sender.Send(context.Background(), Subscription{Endpoint: "http://insecure/x", P256dh: sub.P256dh, Auth: sub.Auth}, nil, time.Hour); err == nil {
		t.Error("an http endpoint was accepted")
	}
}

// checkVAPID verifies the Authorization header like a push service.
func checkVAPID(t *testing.T, header, public, audience string) {
	t.Helper()
	rest, ok := strings.CutPrefix(header, "vapid t=")
	jwt, k, ok2 := strings.Cut(rest, ", k=")
	if !ok || !ok2 || k != public {
		t.Fatalf("authorization header %q", header)
	}
	parts := strings.Split(jwt, ".")
	if len(parts) != 3 {
		t.Fatalf("jwt %q", jwt)
	}
	var claims struct {
		Aud string `json:"aud"`
		Exp int64  `json:"exp"`
		Sub string `json:"sub"`
	}
	raw, _ := unb64(parts[1])
	if err := json.Unmarshal(raw, &claims); err != nil || claims.Aud != audience || claims.Sub != "mailto:admin@example.com" || claims.Exp < time.Now().Unix() {
		t.Errorf("claims %s (aud want %s)", raw, audience)
	}
	pub, _ := unb64(public)
	key, err := ecdh.P256().NewPublicKey(pub)
	if err != nil {
		t.Fatal(err)
	}
	b := key.Bytes()
	var ek ecdsa.PublicKey
	ek.Curve = elliptic.P256()
	ek.X, ek.Y = new(big.Int).SetBytes(b[1:33]), new(big.Int).SetBytes(b[33:])
	sig, _ := unb64(parts[2])
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if len(sig) != 64 || !ecdsa.Verify(&ek, digest[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Error("the VAPID signature doesn't verify")
	}
}
