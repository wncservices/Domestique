package garmin

import "testing"

// Where the ticket sits on the success page is Garmin's business and it has
// moved at least once. Every shape seen so far, plus the ones a small change
// would produce.
func TestTicketIsFoundWhereverItSits(t *testing.T) {
	const want = "ST-01234-abcDEF-cas"

	for _, tc := range []struct{ name, body string }{
		{
			// The original: an embed URL in double quotes.
			"embed url in double quotes",
			`<a href="https://sso.garmin.com/sso/embed?ticket=` + want + `">go</a>`,
		},
		{
			// What broke it: the same URL in a JavaScript string. Garmin
			// returned "Success" and this package called it a bad password.
			"javascript string in single quotes",
			`<script>var response_url = 'https://sso.garmin.com/sso/embed?ticket=` + want + `';</script>`,
		},
		{
			"a different path entirely",
			`<script>window.location = "https://connect.garmin.com/modern?ticket=` + want + `";</script>`,
		},
		{
			"not the first query parameter",
			`<a href="https://sso.garmin.com/sso/embed?service=x&ticket=` + want + `">go</a>`,
		},
		{
			"followed by another parameter",
			`<a href="https://sso.garmin.com/sso/embed?ticket=` + want + `&service=x">go</a>`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := ticketPattern.FindStringSubmatch(tc.body)
			if m == nil {
				t.Fatalf("no ticket found in %s", tc.body)
			}
			if m[1] != want {
				t.Errorf("ticket = %q, want %q", m[1], want)
			}
		})
	}
}

// The sign-in form carries no ticket, and must not appear to.
func TestNoTicketOnTheSignInForm(t *testing.T) {
	body := `<form><input type="hidden" name="_csrf" value="abc"/>
	<input name="username"/><input name="password" type="password"/></form>`
	if m := ticketPattern.FindStringSubmatch(body); m != nil {
		t.Errorf("found a ticket on the sign-in form: %q", m[1])
	}
}
