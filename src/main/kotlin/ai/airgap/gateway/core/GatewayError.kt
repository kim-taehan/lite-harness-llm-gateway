package ai.airgap.gateway.core

/** 모듈이 돌려주는 실패. HTTP 응답으로 바꾸는 곳은 [ErrorResponder] 한 곳이다 */
sealed interface GatewayError {
    /** litellm 은 인증 실패와 관리 라우트 역할 거부를 모두 401 auth_error 로 돌려준다 */
    data class AuthenticationFailed(val reason: String) : GatewayError

    /** 등록되지 않은 경로. litellm(FastAPI) 기본 404 와 같은 모양으로 답한다 */
    data object RouteNotFound : GatewayError
}
