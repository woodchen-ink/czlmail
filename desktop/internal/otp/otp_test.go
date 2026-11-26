package otp

import "testing"

func TestExtract(t *testing.T) {
	cases := []struct {
		name, subject, text, want string
	}{
		{"subject before keyword", "664039 is your password reset code", "", "664039"},
		{"discourse body", "Password reset", "Use this code to reset your password on Discourse Meta:\n\n664039\n\nThis code expires in 10 minutes.", "664039"},
		{"chinese", "登录验证", "您的验证码是 482913，5 分钟内有效。", "482913"},
		{"chinese colon", "【某某】验证码：7731", "", "7731"},
		{"grouped digits", "Sign in", "Your verification code is 123 456.", "123456"},
		{"alphanumeric", "Your one-time code", "Enter K7Q2XM to continue.", "K7Q2XM"},
		{"russian", "Код подтверждения: 5521", "", "5521"},
		{"czech", "Ověřovací kód", "Váš ověřovací kód je 918273.", "918273"},
		{"sentence end", "Security code", "Your security code is 445566.", "445566"},

		{"no keyword", "Invoice 2611200000509", "Amount 1234.56", ""},
		{"promo codes plural", "Your Promo Codes for the App Store", "Dear CZL LTD, a list of the requested promo codes", ""},
		{"year", "Your code", "Valid until 2026.", ""},
		{"amount", "Payment code", "You paid ¥128800 today.", ""},
		{"phone", "Call us", "Verification code hotline 400-820-1234", ""},
		{"date", "Verification code", "Sent 2026/11/26", ""},
		{"barcode", "Barcode 12345678", "", ""},
		{"shipping not pin", "Shipping 445566", "", ""},
		{"uppercase word", "VERIFICATION CODE", "PLEASE CONFIRM", ""},
	}
	for _, c := range cases {
		if got := Extract(c.subject, c.text); got != c.want {
			t.Errorf("%s: Extract(%q, %q) = %q, want %q", c.name, c.subject, c.text, got, c.want)
		}
	}
}

func TestExtractNeedsStrongSubject(t *testing.T) {
	// 泛化词 "code" 出现在主题里不足以把正文第一个数字当验证码。
	if got := Extract("Code review notes", "Ticket 552211 needs a look."); got != "" {
		t.Fatalf("got %q, want empty", got)
	}
}
