package ai.airgap.gateway.auth.internal

import ai.airgap.gateway.auth.Principal
import ai.airgap.gateway.core.Role
import org.springframework.http.HttpHeaders
import org.springframework.stereotype.Component

sealed interface AuthResult {
    data class Authenticated(val principal: Principal) : AuthResult
    data class Rejected(val reason: String) : AuthResult
}

/** 요청 헤더의 키로 신원을 정한다. 지금은 마스터 키만 안다(가상 키는 M2) */
@Component
class Authenticator(properties: MasterKeyProperties) {
    private val masterKey = properties.masterKey

    fun authenticate(headers: HttpHeaders): AuthResult {
        val key = extractKey(headers) ?: return AuthResult.Rejected(NO_KEY)
        if (key.isEmpty()) return AuthResult.Rejected(MALFORMED_KEY)
        if (masterKey.matches(key)) return AuthResult.Authenticated(MASTER_PRINCIPAL)
        return AuthResult.Rejected(INVALID_KEY)
    }

    // litellm 은 Authorization 이 먼저이고 "Bearer "·"bearer " 접두어를 떼며, 접두어가 없으면 값 그대로 쓴다
    private fun extractKey(headers: HttpHeaders): String? {
        val authorization = headers.getFirst(HttpHeaders.AUTHORIZATION)
        if (authorization != null) return authorization.removePrefix("Bearer ").removePrefix("bearer ").trim()
        return headers.getFirst(X_API_KEY)?.trim()
    }

    private companion object {
        const val X_API_KEY = "x-api-key"

        // litellm 메시지와 같게 둔다(auth/user_api_key_auth.py). 앞에 "Authentication Error, " 가 붙는다
        const val NO_KEY = "No api key passed in."
        const val MALFORMED_KEY = "Malformed API Key passed in. Ensure Key has `Bearer ` prefix."
        const val INVALID_KEY = "Invalid proxy server token passed."

        // litellm 은 마스터 키 호출자를 user_id "default_user_id" 의 proxy_admin 으로 본다
        val MASTER_PRINCIPAL = Principal(userId = "default_user_id", role = Role.PROXY_ADMIN)
    }
}
