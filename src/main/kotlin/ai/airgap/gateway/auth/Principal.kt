package ai.airgap.gateway.auth

import ai.airgap.gateway.core.Role
import org.springframework.web.reactive.function.server.ServerRequest

/** 요청 하나 동안 다른 모듈에 넘기는 신원 */
data class Principal(val userId: String, val role: Role) {
    companion object {
        internal const val ATTRIBUTE = "gateway.principal"

        /** 권한 선언이 있는 라우트의 핸들러에서만 부른다. 공개 라우트에는 신원이 없다 */
        fun of(request: ServerRequest): Principal =
            requireNotNull(request.attribute(ATTRIBUTE).orElse(null) as Principal?) {
                "신원이 없다. 공개 라우트에서 부르지 않았는지 확인한다: ${request.path()}"
            }
    }
}
