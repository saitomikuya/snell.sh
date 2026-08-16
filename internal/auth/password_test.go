package auth

import "testing"

func TestPasswordRules(t *testing.T) {
	valid := []string{"四个字符", " pass ", "correct horse battery staple"}
	for _, value := range valid {
		if err := ValidateNewPassword(value); err != nil {
			t.Fatalf("expected %q valid: %v", value, err)
		}
	}
	invalid := []string{"", "abc", DefaultPassword}
	for _, value := range invalid {
		if err := ValidateNewPassword(value); err == nil {
			t.Fatalf("expected %q invalid", value)
		}
	}
}
func TestArgon2idRoundTrip(t *testing.T) {
	encoded, err := HashPassword("测试密码 123")
	if err != nil {
		t.Fatal(err)
	}
	if !VerifyPassword(encoded, "测试密码 123") {
		t.Fatal("password should verify")
	}
	if VerifyPassword(encoded, "wrong") {
		t.Fatal("wrong password verified")
	}
}
