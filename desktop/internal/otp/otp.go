// Package otp 从邮件主题与正文里认出一次性验证码 (登录、注册、重置密码等)。
//
// 只在附近出现验证码类关键词时才认, 宁可漏认也不误认: 列表里把订单号、金额、年份
// 当成验证码显示出来, 比不显示更糟。
package otp

import (
	"regexp"
	"strings"
	"unicode"
)

// scanLimit 是正文里最多看的字符数。验证码都在邮件开头几段, 往后读只会多出误认。
const scanLimit = 2000

// window 是关键词后面最多看的字符数, 关键词前面看得少一些 ("664039 is your code")。
const (
	windowAfter  = 120
	windowBefore = 40
)

// keywords 按小写匹配。覆盖界面支持的语言与常见服务的写法;
// "code" 这类短词只在与其它词组合时才可靠, 单独的 "code" 放在最后兜底。
var keywords = []string{
	"验证码", "驗證碼", "校验码", "校驗碼", "动态码", "動態碼", "确认码", "確認碼", "安全码", "登录码", "激活码", "授权码", "口令",
	"verification code", "verify code", "security code", "confirmation code", "login code", "sign-in code", "signin code",
	"one-time", "one time", "passcode", "otp", "2fa", "two-factor", "authentication code", "access code", "reset code", "pin",
	"ověřovací kód", "bezpečnostní kód", "potvrzovací kód", "jednorázový kód", "kód",
	"код подтверждения", "код проверки", "проверочный код", "одноразовый код", "код",
	"認証コード", "確認コード", "인증번호", "인증 코드",
	"code de vérification", "bestätigungscode", "código de verificación", "codice di verifica",
	"code",
}

// candidate 是 4~8 位数字 (允许中间一个空格或横线分组, 如 "123 456"),
// 或 6~8 位同时含大写字母与数字的串 (如 "K7Q2XM")。前后不能紧挨字母数字。
var candidate = regexp.MustCompile(`(?:^|[^\p{L}\p{N}])(\d{3,4}[ -]\d{3,4}|\d{4,8}|[A-Z0-9]{6,8})(?:$|[^\p{L}\p{N}])`)

// Extract 返回邮件里的验证码, 认不出时为空。主题优先: 很多服务把验证码直接写在主题里。
func Extract(subject, text string) string {
	if c := find(subject); c != "" {
		return c
	}
	if len(text) > scanLimit {
		text = text[:scanLimit]
	}
	if c := find(text); c != "" {
		return c
	}
	// 主题已经说明是验证码邮件 ("Your one-time code"), 正文里只写 "Enter K7Q2XM to continue"
	// 不再重复关键词: 这时取正文开头第一个像验证码的串。泛化词 (code / pin) 不算说明。
	if strongKeyword(subject) {
		return first(text[:min(len(text), firstLimit)])
	}
	return ""
}

// firstLimit 是主题带关键词时正文里找验证码的范围, 只看开头。
const firstLimit = 500

// generic 是单独出现时不足以说明这是验证码邮件的关键词。
var generic = map[string]bool{"code": true, "pin": true, "kód": true, "код": true, "口令": true}

func strongKeyword(s string) bool {
	lower := strings.ToLower(s)
	for _, kw := range keywords {
		if generic[kw] {
			continue
		}
		if i := strings.Index(lower, kw); i >= 0 && isWord(lower, i, len(kw)) {
			return true
		}
	}
	return false
}

func first(s string) string {
	for _, m := range candidate.FindAllStringSubmatchIndex(s, -1) {
		raw := s[m[2]:m[3]]
		if plausible(s, m[2], m[3], raw) {
			return strings.NewReplacer(" ", "", "-", "").Replace(raw)
		}
	}
	return ""
}

func find(s string) string {
	if s == "" {
		return ""
	}
	lower := strings.ToLower(s)
	best, bestDist := "", -1
	for _, kw := range keywords {
		for from := 0; ; {
			i := strings.Index(lower[from:], kw)
			if i < 0 {
				break
			}
			i += from
			from = i + len(kw)
			if !isWord(lower, i, len(kw)) {
				continue
			}
			c, dist := nearest(s, i, i+len(kw))
			if c != "" && (bestDist < 0 || dist < bestDist) {
				best, bestDist = c, dist
			}
		}
		// 越靠前的关键词越具体, 认到了就不再看后面的泛化词。
		if best != "" {
			return best
		}
	}
	return ""
}

// isWord 要求拉丁字母关键词两侧不是字母, 免得 "pin" 命中 "shipping"、"code" 命中 "barcode"。
// 中日韩关键词不分词, 直接认。
func isWord(s string, at, n int) bool {
	first := []rune(s[at : at+n])[0]
	if first > unicode.MaxLatin1 && !unicode.Is(unicode.Latin, first) && !unicode.Is(unicode.Cyrillic, first) {
		return true
	}
	before := lastRune(s[:at])
	after := firstRune(s[at+n:])
	return !unicode.IsLetter(before) && !unicode.IsLetter(after)
}

// nearest 在关键词 [start, end) 前后的窗口里找离它最近的候选, 返回候选与距离。
func nearest(s string, start, end int) (string, int) {
	lo := max(0, start-windowBefore)
	hi := min(len(s), end+windowAfter)
	// 窗口边界可能切在多字节字符中间, 退到字符边界。
	for lo > 0 && !isBoundary(s, lo) {
		lo--
	}
	for hi < len(s) && !isBoundary(s, hi) {
		hi++
	}
	seg := s[lo:hi]
	best, bestDist := "", -1
	for _, m := range candidate.FindAllStringSubmatchIndex(seg, -1) {
		raw := seg[m[2]:m[3]]
		if !plausible(seg, m[2], m[3], raw) {
			continue
		}
		pos := lo + m[2]
		dist := pos - end
		if pos < start {
			dist = (start - (lo + m[3])) * 2 // 关键词前面的候选打折, 正常写法是 "验证码: 123456"。
		}
		if dist < 0 {
			dist = 0
		}
		if bestDist < 0 || dist < bestDist {
			best, bestDist = strings.NewReplacer(" ", "", "-", "").Replace(raw), dist
		}
	}
	return best, bestDist
}

// plausible 排除常见的误认: 年份、金额、时间与日期的一部分、全字母或全数字的大写串里的单词。
func plausible(seg string, from, to int, raw string) bool {
	digits, letters := 0, 0
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
			digits++
		case r >= 'A' && r <= 'Z':
			letters++
		}
	}
	// 字母数字混合串必须同时有字母和数字, 否则 "ACCOUNT" "PASSWORD" 这类大写单词也会被认出来。
	if letters > 0 && digits == 0 {
		return false
	}
	if letters > 0 && digits < 2 {
		return false
	}
	if letters == 0 && digits == 4 {
		if y := raw; y >= "1900" && y <= "2099" {
			return false
		}
	}
	before := []rune(seg[:from])
	after := []rune(seg[to:])
	prev, next := at(before, len(before)-1), at(after, 0)
	// 金额、编号、百分比: ¥123456 / #123456 / 50%。
	if strings.ContainsRune("$¥€£₽#", prev) || next == '%' {
		return false
	}
	// 小数、时间、日期、电话号码的一段: 1,234.56 / 12:30 / 2026/11/26 / 400-820-1234。
	// 只有分隔符另一侧还是数字才算; "验证码是 123456。" 句末的标点不影响。
	const seps = ".,:/-"
	if strings.ContainsRune(seps, prev) && isDigit(at(before, len(before)-2)) {
		return false
	}
	if strings.ContainsRune(seps, next) && isDigit(at(after, 1)) {
		return false
	}
	return true
}

func at(r []rune, i int) rune {
	if i < 0 || i >= len(r) {
		return ' '
	}
	return r[i]
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isBoundary(s string, i int) bool {
	return i == 0 || i == len(s) || (s[i]&0xC0) != 0x80
}

func lastRune(s string) rune {
	r := []rune(s)
	if len(r) == 0 {
		return ' '
	}
	return r[len(r)-1]
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return ' '
}
