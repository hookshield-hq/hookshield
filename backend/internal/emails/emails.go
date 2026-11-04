// Package emails renders the account emails (verification, password reset,
// password changed) in the user's language: en, pt-BR or es.
// Every template must exist in all three languages (enforced by tests).
package emails

import (
	"fmt"
	"strings"
)

// Kind identifies a template.
type Kind string

// Templates.
const (
	VerifyEmail     Kind = "verify_email"
	ResetPassword   Kind = "reset_password"
	PasswordChanged Kind = "password_changed"
)

type template struct {
	subject string
	body    string // fmt with: link (empty for PasswordChanged)
}

var catalog = map[string]map[Kind]template{
	"en": {
		VerifyEmail: {"Confirm your HookShield email", `Welcome to HookShield!

Confirm your email address by opening this link (valid for 48 hours):

%s

If you did not create an account, you can ignore this email.`},
		ResetPassword: {"Reset your HookShield password", `We received a request to reset your HookShield password.

Choose a new password with this link (valid for 1 hour):

%s

If you did not ask for it, ignore this email — your password stays the same.`},
		PasswordChanged: {"Your HookShield password was changed", `The password of your HookShield account was just changed and every other session was signed out.

If this was not you, reset your password immediately and contact support.%s`},
	},
	"pt-BR": {
		VerifyEmail: {"Confirme seu e-mail no HookShield", `Boas-vindas ao HookShield!

Confirme seu endereço de e-mail abrindo este link (válido por 48 horas):

%s

Se você não criou uma conta, ignore este e-mail.`},
		ResetPassword: {"Redefina sua senha do HookShield", `Recebemos um pedido para redefinir a senha da sua conta HookShield.

Escolha uma nova senha por este link (válido por 1 hora):

%s

Se não foi você, ignore este e-mail — sua senha continua a mesma.`},
		PasswordChanged: {"Sua senha do HookShield foi alterada", `A senha da sua conta HookShield acabou de ser alterada e todas as outras sessões foram encerradas.

Se não foi você, redefina sua senha imediatamente e fale com o suporte.%s`},
	},
	"es": {
		VerifyEmail: {"Confirma tu correo en HookShield", `¡Bienvenido a HookShield!

Confirma tu dirección de correo abriendo este enlace (válido por 48 horas):

%s

Si no creaste una cuenta, ignora este correo.`},
		ResetPassword: {"Restablece tu contraseña de HookShield", `Recibimos una solicitud para restablecer la contraseña de tu cuenta HookShield.

Elige una nueva contraseña con este enlace (válido por 1 hora):

%s

Si no fuiste tú, ignora este correo: tu contraseña no cambia.`},
		PasswordChanged: {"Tu contraseña de HookShield fue cambiada", `La contraseña de tu cuenta HookShield acaba de cambiar y se cerraron todas las demás sesiones.

Si no fuiste tú, restablece tu contraseña de inmediato y contacta con soporte.%s`},
	},
}

// Locales lists the supported languages.
func Locales() []string { return []string{"en", "pt-BR", "es"} }

func normalize(locale string) string {
	if _, ok := catalog[locale]; ok {
		return locale
	}
	switch strings.ToLower(strings.SplitN(locale, "-", 2)[0]) {
	case "pt":
		return "pt-BR"
	case "es":
		return "es"
	}
	return "en"
}

// Render returns subject and plain-text body. link is the action URL
// (ignored by PasswordChanged).
func Render(kind Kind, locale, link string) (subject, body string, err error) {
	t, ok := catalog[normalize(locale)][kind]
	if !ok {
		return "", "", fmt.Errorf("emails: unknown template %q", kind)
	}
	arg := link
	if kind == PasswordChanged {
		arg = ""
	}
	return t.subject, fmt.Sprintf(t.body, arg) + "\n\n— HookShield\n", nil
}
