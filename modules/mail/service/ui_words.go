package main

// Small server-rendered pages do not depend on the browser application's JS.
var serverWords = map[string][5]string{
	"en":    {"Continue", "Signed out", "Mail and central sign-out completed.", "Local mail session ended. Central sign-out could not be confirmed.", "Sign in"},
	"zh-CN": {"继续", "已退出登录", "邮件与统一身份会话已退出。", "本地邮件会话已退出，尚未确认统一退出。", "登录"},
	"zh-TW": {"繼續", "已登出", "郵件與統一身分工作階段已登出。", "本機郵件已登出，尚未確認統一登出。", "登入"},
	"de":    {"Weiter", "Abgemeldet", "Mail und zentrale Sitzung wurden beendet.", "Lokal abgemeldet. Die zentrale Abmeldung konnte nicht bestätigt werden.", "Anmelden"},
	"es":    {"Continuar", "Sesión cerrada", "Las sesiones de correo y central se han cerrado.", "Sesión local cerrada. No se pudo confirmar el cierre central.", "Iniciar sesión"},
	"fr":    {"Continuer", "Déconnecté", "Les sessions de messagerie et centrale sont fermées.", "Session locale fermée. La déconnexion centrale n’a pas pu être confirmée.", "Se connecter"},
	"ja":    {"続ける", "ログアウトしました", "メールと統合認証からログアウトしました。", "メールからログアウトしました。統合ログアウトは確認できませんでした。", "ログイン"},
	"ko":    {"계속", "로그아웃 완료", "메일 및 통합 인증에서 로그아웃했습니다.", "메일에서 로그아웃했습니다. 통합 로그아웃은 확인되지 않았습니다.", "로그인"},
}

func (s *loginHandler) words() [5]string {
	words, ok := serverWords[s.language]
	if !ok {
		return serverWords["en"]
	}
	return words
}
