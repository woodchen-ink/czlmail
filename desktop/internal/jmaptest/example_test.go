package jmaptest_test

import (
	"testing"

	"github.com/woodchen-ink/czlmail/desktop/internal/jmaptest"
)

// 两台服务器的账号 id 撞车, 同一台服务器上两个用户共享一个账号。
func ExampleNew() {
	t := &testing.T{} // 实际测试里用测试函数的 t
	one := jmaptest.New(t)
	alice := one.AddUser("alice@one.test", "pw-alice")
	bob := one.AddUser("bob@one.test", "pw-bob")
	a := one.AddAccount("a", "alice@one.test", jmaptest.AllCaps...)
	b := one.AddAccount("b", "bob@one.test")
	team := one.AddAccount("t", "team@one.test")
	alice.Grant(a, true).Grant(team, false)
	bob.Grant(b, true).Grant(team, false)

	two := jmaptest.New(t)
	carol := two.AddUser("carol@two.test", "pw-carol")
	carol.Grant(two.AddAccount("a", "carol@two.test"), true) // 与 one 上的 "a" 撞车

	a.AddEmail(jmaptest.Email{Subject: "hello", From: "x@ext.test"})
	team.AddEmail(jmaptest.Email{Subject: "for the team", From: "y@ext.test", Seen: true})

	// 客户端以 one.URL() 作服务器地址、用户名 + 密码登录; 之后:
	_ = one.CallsTo("Email/set")      // 断言写操作发到了这台服务器、用的是服务器账号 id
	_ = one.Submissions()             // 发出的邮件
	one.SetMode(jmaptest.Unavailable) // 503; jmaptest.Down 直接断连接; jmaptest.Online 恢复
	alice.Reject(true)                // 401: 应用专用密码被撤销
	bob.Revoke(team)                  // 撤销共享, 下一次读会话时账号消失
	one.OnMethod(func(c jmaptest.Call) {
		// 在方法执行前卡住它 (比如等测试移除登录), 复现竞态
	})
}
