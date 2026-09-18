package htmltext

import "testing"

func TestConvert(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"块级元素换行", "<p>第一段</p><p>第二段</p>", "第一段\n第二段"},
		{"br", "a<br>b<br />c", "a\nb\nc"},
		{"丢掉样式与脚本", "<style>.a{color:red}</style><script>x=1</script><p>正文</p>", "正文"},
		{"丢掉注释", "<!--[if mso]>outlook<![endif]--><p>正文</p>", "正文"},
		{"解实体", "<p>a &amp; b &lt;c&gt; &nbsp;d &hellip;</p>", "a & b <c> d …"},
		{"实体里的标签不再当标签", "&lt;script&gt;alert(1)&lt;/script&gt;", "<script>alert(1)</script>"},
		{"列表", "<ul><li>一</li><li>二</li></ul>", "一\n二"},
		{"压掉空行", "<p>a</p><br><br><br><br><p>b</p>", "a\n\nb"},
		{"表格", "<table><tr><td>甲</td><td>乙</td></tr><tr><td>丙</td></tr></table>", "甲乙\n丙"},
		{"纯文本原样", "没有标签的文本", "没有标签的文本"},
		{"空", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Convert(c.in); got != c.want {
				t.Errorf("Convert(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
