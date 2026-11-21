// Package admin 은 litellm 대시보드가 부르는 관리 API 다. 경로·요청·응답 모양을 litellm 과 같게 맞추고,
// 여러 모듈(auth 신원 + limits 한도)의 값을 합친 응답을 여기서 조립한다(설계 3장). 자기 테이블은 아직 없다(audit_log 를 넣으면 하나가 된다).
package admin

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"

	"llm-gateway/internal/core"
)

// FieldError 는 pydantic v2 검증 오류 항목이다(FastAPI 422 본문 {"detail":[…]} 의 원소)
type FieldError struct {
	Type  string `json:"type"`
	Loc   []any  `json:"loc"`
	Msg   string `json:"msg"`
	Input any    `json:"input"`
}

type validationError []FieldError

func (v validationError) write(w http.ResponseWriter) {
	core.DetailError{Status: http.StatusUnprocessableEntity, Detail: []FieldError(v)}.Write(w)
}

// requestBody 는 JSON 본문을 dst 로 읽는다. 형식이 틀리면 FastAPI 와 같은 422 를 쓰고 false 를 돌려준다.
// raw 는 "필드 없음" 오류의 input 으로 쓰는 원본(객체)이다
func requestBody(w http.ResponseWriter, r *http.Request, dst any) (raw map[string]any, ok bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 10<<20))
	if err != nil {
		validationError{{Type: "json_invalid", Loc: []any{"body"}, Msg: "JSON decode error", Input: nil}}.write(w)
		return nil, false
	}
	if err := json.Unmarshal(data, &raw); err != nil || raw == nil {
		validationError{{Type: "model_attributes_type", Loc: []any{"body"},
			Msg: "Input should be a valid dictionary or object to extract fields from", Input: string(data)}}.write(w)
		return nil, false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(dst); err != nil {
		validationError{typeError(err)}.write(w)
		return nil, false
	}
	return raw, true
}

// typeError 는 encoding/json 의 형식 오류를 pydantic 오류 하나로 바꾼다
func typeError(err error) FieldError {
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		return FieldError{Type: "json_invalid", Loc: []any{"body"}, Msg: "JSON decode error"}
	}
	loc := []any{"body"}
	for _, part := range strings.Split(typeErr.Field, ".") {
		if part != "" {
			loc = append(loc, part)
		}
	}
	kind, msg := "string_type", "Input should be a valid string"
	switch typeErr.Type.Kind() {
	case reflect.Slice:
		kind, msg = "list_type", "Input should be a valid list"
	case reflect.Map:
		kind, msg = "dict_type", "Input should be a valid dictionary"
	case reflect.Bool:
		kind, msg = "bool_type", "Input should be a valid boolean"
	case reflect.Float64, reflect.Float32:
		kind, msg = "float_type", "Input should be a valid number"
	case reflect.Int, reflect.Int64, reflect.Int32:
		kind, msg = "int_type", "Input should be a valid integer"
	}
	return FieldError{Type: kind, Loc: loc, Msg: msg, Input: typeErr.Value}
}

func missing(raw map[string]any, field string) FieldError {
	return FieldError{Type: "missing", Loc: []any{"body", field}, Msg: "Field required", Input: raw}
}

// queryInt 는 정수 쿼리 파라미터다. 범위 밖이면 FastAPI 와 같은 오류를 errs 에 더하고 기본값을 쓴다
func queryInt(r *http.Request, name string, def, min, max int, errs *validationError) int {
	raw := r.URL.Query().Get(name)
	if raw == "" {
		return def
	}
	v, err := strconv.Atoi(raw)
	loc := []any{"query", name}
	switch {
	case err != nil:
		*errs = append(*errs, FieldError{Type: "int_parsing", Loc: loc, Msg: "Input should be a valid integer, unable to parse string as an integer", Input: raw})
	case v < min:
		*errs = append(*errs, FieldError{Type: "greater_than_equal", Loc: loc, Msg: "Input should be greater than or equal to " + strconv.Itoa(min), Input: raw})
	case max > 0 && v > max:
		*errs = append(*errs, FieldError{Type: "less_than_equal", Loc: loc, Msg: "Input should be less than or equal to " + strconv.Itoa(max), Input: raw})
	default:
		return v
	}
	return def
}

// queryString 은 비어 있지 않은 쿼리 값이다. 없으면 nil
func queryString(r *http.Request, name string) *string {
	if v := r.URL.Query().Get(name); v != "" {
		return &v
	}
	return nil
}

// queryList 는 쉼표로 이은 목록이다(litellm user_ids 등)
func queryList(r *http.Request, name string) []string {
	var result []string
	for _, part := range strings.Split(r.URL.Query().Get(name), ",") {
		if p := strings.TrimSpace(part); p != "" {
			result = append(result, p)
		}
	}
	return result
}

// litellm 은 dict 를 담은 HTTPException 을 ProxyException 으로 감쌀 때 message 를 파이썬 repr 문자열로 만든다.
// 대시보드는 이 문자열을 정규식으로 풀어 메시지를 보여 주므로(unwrapProxyErrorMessage) 같은 모양을 낸다
func pyDict(key, message string) string { return "{" + pyString(key) + ": " + pyString(message) + "}" }

// 파이썬 repr: 작은따옴표가 들어 있고 큰따옴표가 없으면 큰따옴표로 감싼다. 아니면 작은따옴표로 감싸고 ' 를 이스케이프한다
func pyString(v string) string {
	escaped := strings.NewReplacer(`\`, `\\`, "\n", `\n`).Replace(v)
	if strings.Contains(v, "'") && !strings.Contains(v, `"`) {
		return `"` + escaped + `"`
	}
	return "'" + strings.ReplaceAll(escaped, "'", `\'`) + "'"
}

// proxyError 는 (A) 모양 오류다. message 는 litellm 처럼 {'error': …} repr 로 감싼다
func proxyError(status int, message string) core.ProxyError {
	return core.ProxyError{Status: status, Message: pyDict("error", message), Type: "internal_server_error"}
}

// detailError 는 (B) 모양 {"detail":{"error": …}} 이다
func detailError(status int, message string) core.DetailError {
	return core.DetailMessage(status, message)
}

// authStyle 은 litellm 이 auth_error·param "None" 으로 감싸는 오류다(/team/info, /key/update, /user/update 등).
// message 는 부르는 쪽이 그대로 정한다(repr 로 감쌀지는 엔드포인트마다 다르다)
func authStyle(status int, message string) core.ProxyError {
	return core.ProxyError{Status: status, Message: message, Type: "auth_error", Param: "None"}
}
