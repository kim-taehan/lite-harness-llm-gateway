package auth

import (
	"html/template"
	"net/http"
)

// 브라우저 화면. 문구와 폼 필드 이름은 litellm(K 2.5·6.2)과 같다. e2e 시험이 필드 이름(browser_complete_token, user_code)으로 폼을 읽는다

var pageTemplates = template.Must(template.New("").Parse(`
{{define "head"}}<!doctype html><html lang="ko"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title><style>
body{font-family:system-ui,-apple-system,sans-serif;background:#f6f7f9;color:#1f2328;margin:0;display:flex;min-height:100vh;align-items:center;justify-content:center}
main{background:#fff;border:1px solid #d8dee4;border-radius:12px;padding:32px;max-width:380px;width:calc(100% - 32px)}
h1{font-size:20px;margin:0 0 12px}p{color:#59636e;font-size:14px;line-height:1.5}
label{display:block;font-size:13px;margin:12px 0 4px}input{width:100%;box-sizing:border-box;padding:9px 10px;border:1px solid #d1d9e0;border-radius:6px;font-size:14px}
button,a.button{display:block;width:100%;box-sizing:border-box;margin-top:16px;padding:10px;border:0;border-radius:6px;background:#1f6feb;color:#fff;font-size:14px;text-align:center;text-decoration:none;cursor:pointer}
a.button{background:#24292f}.or{text-align:center;color:#8c959f;font-size:12px;margin:16px 0 0}.error{color:#cf222e}
</style></head><body><main>{{end}}
{{define "tail"}}</main></body></html>{{end}}

{{define "signin"}}{{template "head" .}}
<h1>Sign in to LiteLLM</h1>
{{if .SSO}}<a class="button" href="{{.SSOURL}}">회사 계정(SSO)으로 로그인</a>{{if .Password}}<p class="or">또는</p>{{end}}{{end}}
{{if .Password}}{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<form method="post" action="{{.Action}}">
<label for="username">이메일 또는 아이디</label><input id="username" name="username" type="text" autocomplete="username" required>
<label for="password">비밀번호</label><input id="password" name="password" type="password" autocomplete="current-password" required>
<button type="submit">로그인</button></form>{{end}}
{{template "tail" .}}{{end}}

{{define "complete"}}{{template "head" .}}
<h1>Complete CLI Login</h1>
<p>Enter the verification code shown in your terminal to finish signing in.</p>
{{if .Error}}<p class="error">{{.Error}}</p>{{end}}
<form method="post" action="{{.Action}}">
<input type="hidden" name="browser_complete_token" value="{{.Token}}">
<label for="user_code">Verification code</label><input id="user_code" name="user_code" type="text" autocomplete="one-time-code" required>
<button type="submit">Continue</button></form>
{{template "tail" .}}{{end}}

{{define "success"}}{{template "head" .}}
<h1>Authentication Successful!</h1>
<p>로그인이 끝났습니다. 이 창을 닫고 앱으로 돌아가세요.</p>
<script>window.close();</script>
{{template "tail" .}}{{end}}
`))

type page struct {
	Title    string
	Action   string
	Token    string
	Error    string
	SSO      bool
	Password bool
	SSOURL   string
}

func writePage(w http.ResponseWriter, status int, name string, p page) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = pageTemplates.ExecuteTemplate(w, name, p)
}
