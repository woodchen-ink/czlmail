package syncer

import (
	"context"
	"database/sql"
)

// 同步器的寿命跟着一次会话: 退出登录、凭据失效、重新登录 (换上新会话)、移除登录时停用。
// 停用后它不再发请求、不再写库。只取消同步循环的 context 不够: 用户发起的后台拉取 (翻页补取、拉取整个文件夹、
// 预取正文、按 id 补邮件) 用的是应用的 context, 会接着拿旧会话访问服务器并写库; 移除登录时它们写回的行
// 落在已删掉的前缀下, 成了没有账号行的孤儿数据。

// CodeStopped 是同步器已停用后再调用它的错误码。
const CodeStopped = 1106

var errStopped = &Error{Code: CodeStopped, Msg: "sync session closed"}

// Stop 停用同步器, 不等在途的操作: 之后的请求与写库一律报 1106。
func (s *Syncer) Stop() { s.stopped.Store(true) }

// Close 停用同步器并等在途的写库做完。删这个会话的缓存之前调用, 删完之后不会再有迟到的写入。
func (s *Syncer) Close() {
	s.Stop()
	s.writes.Lock()
	s.writes.Unlock()
}

// Stopped 报告同步器是否已停用。
func (s *Syncer) Stopped() bool { return s.stopped.Load() }

// writing 在同步器未停用时执行一次写库。写库之间不能嵌套调用 (读写锁不可重入)。
func (s *Syncer) writing(fn func() error) error {
	s.writes.RLock()
	defer s.writes.RUnlock()
	if s.stopped.Load() {
		return errStopped
	}
	return fn()
}

// write 是 writing 的事务版本, 同步器写缓存一律经它。
func (s *Syncer) write(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.writing(func() error { return s.store.WithTx(ctx, fn) })
}
