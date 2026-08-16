package laravel

import (
	"strings"
	"testing"
)

// Fixtures below were produced by the real Illuminate\Encryption\Encrypter
// (the class `php artisan env:encrypt` uses), so these tests pin us to
// Laravel's actual output rather than to our own re-implementation.
const (
	fixturePlaintext = "APP_NAME=\"Neo Demo\"\n" +
		"APP_ENV=production\n" +
		"APP_KEY=base64:8Zm1kQ0Zx3JbCq5r7Tn2Vw9Xy4Lp6Rs8Td0Uv2Wx4Y=\n" +
		"APP_DEBUG=false\n" +
		"# comment line\n" +
		"DB_PASSWORD='p@ss word#1'\n" +
		"UNICODE_NOTE=中文測試"

	fixtureKey256 = "base64:AQIDBAUGBwgBAgMEBQYHCAECAwQFBgcIAQIDBAUGBwg="
	fixtureKey128 = "base64:CgsMDQoLDA0KCwwNCgsMDQ=="

	fixtureCBC256 = "eyJpdiI6IkgyeDJzelRtMFQzaExNTGhJZElSREE9PSIsInZhbHVlIjoiTFZsZExvUzRhekx3WUdIUi9BSm05VTZPWWtRZUtzTUU3QitkZVBucXAyZHh3SnJ2SFNidVR6OTd2UGtsUXNYdDY3NGFVQytJb0RyRjZaQ3NXaVdBMWhOTVAxMFRLSEY3VG9VYS96ZnZvY3NEenQ3MzRNVHQ4TDdKSC8zcmxjUWVhWFhaQS96ZU5WNUNwQUpXaVNWUVl2SE4xTCtvTE1NYVdIWVcxZXVKUmtNaDRib2xIVlF0ZFl6bm94UUlJcHJRRnZGMElUUHdPODRhK2NrSFBjNXc4Rm42NUlXeDlWeGhLK2lHRjBFcUJLaU53MUZqR09LZkVSWGdObWFsV0ZZNCIsIm1hYyI6IjYzM2M3Mjg5NGEwYzg0NjFlMmJlNDg5MTQ5NWQyZWRmY2MzYzc0ODM1NDJhNmRlMWM0ODcwNTdiMmVmNWRjMTEiLCJ0YWciOiIifQ=="
	fixtureCBC128 = "eyJpdiI6IkdYRFFUMG1FQWpIazVsYlcvUnFNSkE9PSIsInZhbHVlIjoiRCtSR1VBbzdQblRhTUlONlBUWk1hT1JuNlVLMllReDd5clY4b0xkV3Z1QUlDZ2lLOW45bUhqWmhMUUpqckhyUGJOL0FuelJtOFplN0N1UjVQdkV6M0Z2R21hVmhyQ1RneG5VTk9xa0JRK1JDMUI3S1M4R3B3THBWdEo2Y3BKYVlLN1d0V3J3VytiZEF6YkZYcmJrOW4rV05XN2RQVnVTVjZNQklvVmhSUTZmaStncDA4ajlzbXc5Zm1ONjhhMjU1OXZlajVER2FUSnhXS1hJTWV6dS94dzdJRWJDTEwvY2p5aEx3Ly93bWViOXJqRWVYbFdvbUVLZjRwdWozODhQcyIsIm1hYyI6IjI4OTBhM2VlYzY2YWM0N2FlMGQxOWM1YTcwZWQ1OWRlNmYxNjA2MzY4OWI0ODI3NmVmMzlkOGVkMWI3Y2E5ODciLCJ0YWciOiIifQ=="
	fixtureGCM256 = "eyJpdiI6Ikh5VDdLK2orUndzcW4veEQiLCJ2YWx1ZSI6IjEyVlRIV1hnT21DYmNlYUZpZVNDaXVwVGp3VUw3SU11VG9obEtLZmM4aFQ3UUw4Y2psU2EzeFFDUy9PeTNxYTFrYm9xSHNTNEZCeXdld05rZmhLSTVsa3MreklJR0hsRTdMb2kzT0FMRU1GUHJnLy91ZUdBdDBlbUg0RjZMTnIrQlV5OHFLczFtbnFkVUxvWWhBNVU3dStuMFpVcDdEZWhaWFBiWThtdGduQnd4aGl1V2QxdUVxR2xyY1U4aExYdGl3MGJtSFpXUm5aZC9zVzVKQ1V5NUFNdTQyR2hEVnlSRGxQVEJrV2NLNWI0UWRUWFBnOUw2blR1WG1nRCIsIm1hYyI6IiIsInRhZyI6Ilc2ZFozdjdlM3BFU2tvcXZTQ0tqOHc9PSJ9"
	// encryptString() output — no PHP serialize() wrapper.
	fixtureCBC256Raw = "eyJpdiI6IkRpZWFqYTQvMHNqKzBuVnpIdDJaRVE9PSIsInZhbHVlIjoia3YzeVAyeUthUC9MdXV6WTNiMjVrK1pDYVZveUJNOUdQQ0JxM1crU2Vqa0x5UVM1SlZWQ2RSbVlrYVlENUoyc1JvQlhuS2NjTVRKcG1FQXRDUUV0RmRtTUd4QmtBWXI1TFg5UG9YY1lDeHdzaWpQUjBjaEUxWXpGb2p0bWQzTmJsbXd0MXJ2L005RFprUHBpTDMycHZZcmR6RU56bExuSXE1VEFINVNJTEdZdTVFa0ZhYlh4N3o2NWRkMjdpZHNYRkZkTklDQjNSRXc2MS95OHV5OHFyd0ZRV0hLQmNzNTB1c1NUUjdMVVVRemJyblVyZkQ4WGZCbGhvdCswOEhyNyIsIm1hYyI6IjA4YTVmODdkZDljMTc3ODMyNzBkZTkzNWIzMmNmNmJkMzMzMGY4MjI3YTgzMDQ5ZjFiOTU0NWEyZGQ5YjM3ZDEiLCJ0YWciOiIifQ=="
)

func mustKey(t *testing.T, s string) []byte {
	t.Helper()
	k, err := ParseKey(s)
	if err != nil {
		t.Fatalf("ParseKey(%q): %v", s, err)
	}
	return k
}

func TestDecryptLaravelFixtures(t *testing.T) {
	cases := []struct {
		name    string
		payload string
		key     string
	}{
		{"aes-256-cbc", fixtureCBC256, fixtureKey256},
		{"aes-128-cbc", fixtureCBC128, fixtureKey128},
		{"aes-256-gcm", fixtureGCM256, fixtureKey256},
		{"aes-256-cbc unserialized", fixtureCBC256Raw, fixtureKey256},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := Decrypt(tc.payload, mustKey(t, tc.key))
			if err != nil {
				t.Fatalf("Decrypt: %v", err)
			}
			if got != fixturePlaintext {
				t.Errorf("plaintext mismatch\n got: %q\nwant: %q", got, fixturePlaintext)
			}
		})
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	// A valid-length key that isn't the right one must fail the MAC check
	// rather than returning garbage.
	wrong := mustKey(t, "base64:"+strings.Repeat("A", 43)+"=")
	if _, err := Decrypt(fixtureCBC256, wrong); err == nil {
		t.Fatal("expected wrong key to fail")
	}
}

func TestDecryptTamperedPayloadFails(t *testing.T) {
	// Flip a byte inside the base64 ciphertext; the MAC must catch it.
	tampered := strings.Replace(fixtureCBC256, "TFZsZEx", "TFZsZEy", 1)
	if tampered == fixtureCBC256 {
		t.Skip("fixture layout changed; nothing tampered")
	}
	if _, err := Decrypt(tampered, mustKey(t, fixtureKey256)); err == nil {
		t.Fatal("expected tampered payload to fail")
	}
}

func TestDecryptGCMTamperedTagFails(t *testing.T) {
	tampered := strings.Replace(fixtureGCM256, "Ilc2ZFoz", "Ilc2ZFo0", 1)
	if tampered == fixtureGCM256 {
		t.Skip("fixture layout changed; nothing tampered")
	}
	if _, err := Decrypt(tampered, mustKey(t, fixtureKey256)); err == nil {
		t.Fatal("expected tampered GCM tag to fail")
	}
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	for _, keyStr := range []string{fixtureKey128, fixtureKey256} {
		key := mustKey(t, keyStr)
		payload, err := Encrypt(fixturePlaintext, key)
		if err != nil {
			t.Fatalf("Encrypt: %v", err)
		}
		got, err := Decrypt(payload, key)
		if err != nil {
			t.Fatalf("Decrypt: %v", err)
		}
		if got != fixturePlaintext {
			t.Errorf("round trip mismatch\n got: %q\nwant: %q", got, fixturePlaintext)
		}
	}
}

func TestEncryptProducesFreshIV(t *testing.T) {
	key := mustKey(t, fixtureKey256)
	a, err := Encrypt(fixturePlaintext, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	b, err := Encrypt(fixturePlaintext, key)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if a == b {
		t.Error("two encryptions of the same plaintext produced identical payloads — IV is not random")
	}
}

func TestParseKey(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		wantLen int
		wantErr bool
	}{
		{"base64 prefixed 32", fixtureKey256, 32, false},
		{"base64 prefixed 16", fixtureKey128, 16, false},
		{"raw 32 bytes", strings.Repeat("k", 32), 32, false},
		{"raw 16 bytes", strings.Repeat("k", 16), 16, false},
		{"base64 without prefix", "AQIDBAUGBwgBAgMEBQYHCAECAwQFBgcIAQIDBAUGBwg=", 32, false},
		{"whitespace trimmed", "  " + fixtureKey256 + "\n", 32, false},
		{"empty", "", 0, true},
		{"wrong length", "too-short", 0, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseKey(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseKey: %v", err)
			}
			if len(got) != tc.wantLen {
				t.Errorf("key length = %d, want %d", len(got), tc.wantLen)
			}
		})
	}
}

func TestGenerateKey(t *testing.T) {
	for _, size := range []int{16, 32} {
		gen, err := GenerateKey(size)
		if err != nil {
			t.Fatalf("GenerateKey(%d): %v", size, err)
		}
		if !strings.HasPrefix(gen, "base64:") {
			t.Errorf("generated key %q missing base64: prefix", gen)
		}
		parsed, err := ParseKey(gen)
		if err != nil {
			t.Fatalf("ParseKey(generated): %v", err)
		}
		if len(parsed) != size {
			t.Errorf("generated key length = %d, want %d", len(parsed), size)
		}
	}
	if _, err := GenerateKey(24); err == nil {
		t.Error("expected error for unsupported key size")
	}
}

func TestDecryptRejectsGarbage(t *testing.T) {
	key := mustKey(t, fixtureKey256)
	for _, in := range []string{"", "not base64!!!", "aGVsbG8gd29ybGQ="} {
		if _, err := Decrypt(in, key); err == nil {
			t.Errorf("expected error decrypting %q", in)
		}
	}
}
