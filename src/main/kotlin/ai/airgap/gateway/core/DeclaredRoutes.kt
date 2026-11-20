package ai.airgap.gateway.core

import org.springframework.web.reactive.function.server.RouterFunction
import org.springframework.web.reactive.function.server.ServerRequest
import org.springframework.web.reactive.function.server.ServerResponse
import org.springframework.web.reactive.function.server.coRouter

/** litellm 역할 문자열. 대시보드가 이 값으로 메뉴를 나누므로 철자를 바꾸지 않는다 */
enum class Role(val value: String) {
    PROXY_ADMIN("proxy_admin"),
    PROXY_ADMIN_VIEWER("proxy_admin_viewer"),
    INTERNAL_USER("internal_user"),
    INTERNAL_USER_VIEWER("internal_user_viewer"),
    TEAM("team"),
}

sealed interface Access {
    data object Public : Access
    data class Roles(val roles: Set<Role>) : Access

    companion object {
        fun roles(vararg roles: Role): Access = Roles(roles.toSet())
    }
}

enum class Method { GET, POST, PUT, PATCH, DELETE }

data class RouteRule(val method: Method, val path: String, val access: Access)

/**
 * 권한 선언이 붙은 라우트. 라우트와 권한을 한 곳에서 함께 선언해야 둘이 어긋나지 않는다.
 * 모듈은 라우트를 반드시 이 타입으로 등록하고, auth 의 필터가 [rules] 로 검사한다.
 */
class DeclaredRoutes internal constructor(
    router: RouterFunction<ServerResponse>,
    val rules: List<RouteRule>,
) : RouterFunction<ServerResponse> by router

typealias Handler = suspend (ServerRequest) -> ServerResponse

class DeclaredRoutesBuilder internal constructor() {
    private val routes = mutableListOf<Pair<RouteRule, Handler>>()

    fun GET(path: String, access: Access, handler: Handler) = add(Method.GET, path, access, handler)
    fun POST(path: String, access: Access, handler: Handler) = add(Method.POST, path, access, handler)
    fun PUT(path: String, access: Access, handler: Handler) = add(Method.PUT, path, access, handler)
    fun PATCH(path: String, access: Access, handler: Handler) = add(Method.PATCH, path, access, handler)
    fun DELETE(path: String, access: Access, handler: Handler) = add(Method.DELETE, path, access, handler)

    private fun add(method: Method, path: String, access: Access, handler: Handler) {
        routes += RouteRule(method, path, access) to handler
    }

    internal fun build(): DeclaredRoutes {
        val router = coRouter {
            routes.forEach { (rule, handler) ->
                when (rule.method) {
                    Method.GET -> GET(rule.path, handler)
                    Method.POST -> POST(rule.path, handler)
                    Method.PUT -> PUT(rule.path, handler)
                    Method.PATCH -> PATCH(rule.path, handler)
                    Method.DELETE -> DELETE(rule.path, handler)
                }
            }
        }
        return DeclaredRoutes(router, routes.map { it.first })
    }
}

fun declaredRoutes(block: DeclaredRoutesBuilder.() -> Unit): DeclaredRoutes =
    DeclaredRoutesBuilder().apply(block).build()
