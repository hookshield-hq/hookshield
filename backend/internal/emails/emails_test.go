package emails

import (
	"strings"
	"testing"
)

func TestEveryTemplateInEveryLocale(t *testing.T) {
	for _, loc := range Locales() {
		for _, kind := range []Kind{VerifyEmail, ResetPassword, PasswordChanged} {
			tpl, ok := catalog[loc][kind]
			if !ok || tpl.subject == "" || strings.Count(tpl.body, "%s") != 1 {
				t.Errorf("%s/%s missing or without exactly one %%s placeholder", loc, kind)
			}
		}
		if len(catalog[loc]) != len(catalog["en"]) {
			t.Errorf("%s has %d templates, en has %d", loc, len(catalog[loc]), len(catalog["en"]))
		}
	}
}

func TestRender(t *testing.T) {
	subj, body, err := Render(VerifyEmail, "pt-BR", "https://app/verify?token=abc")
	if err != nil || subj != "Confirme seu e-mail no HookShield" || !strings.Contains(body, "https://app/verify?token=abc") {
		t.Fatalf("%q %q %v", subj, body, err)
	}
	subj, _, _ = Render(ResetPassword, "es-AR", "x")
	if subj != "Restablece tu contraseña de HookShield" {
		t.Fatalf("es fallback: %q", subj)
	}
	subj, body, _ = Render(PasswordChanged, "fr", "ignored")
	if subj != "Your HookShield password was changed" || strings.Contains(body, "ignored") || strings.Contains(body, "%!") {
		t.Fatalf("%q %q", subj, body)
	}
	if _, _, err := Render("nope", "en", ""); err == nil {
		t.Fatal("unknown template accepted")
	}
}
