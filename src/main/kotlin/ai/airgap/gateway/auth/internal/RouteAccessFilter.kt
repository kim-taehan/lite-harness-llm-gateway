package ai.airgap.gateway.auth.internal

import ai.airgap.gateway.auth.Principal
import ai.airgap.gateway.core.Access
import ai.airgap.gateway.core.DeclaredRoutes
import ai.airgap.gateway.core.ErrorResponder
import ai.airgap.gateway.core.GatewayError
import ai.airgap.gateway.core.RouteRule
import org.springframework.stereotype.Component
import org.springframework.web.server.CoWebFilter
import org.springframework.web.server.CoWebFilterChain
import org.springframework.web.server.ServerWebExchange
import org.springframework.web.util.pattern.PathPattern
import org.springframework.web.util.pattern.PathPatternParser

/**
 * 모든 요청을 라우트 권한 선언으로 검사한다. 선언이 없는 경로는 핸들러에 닿기 전에 404 로 끝낸다.
 * 그래서 권한 선언을 빠뜨린 라우트는 열리지 않고 아예 동작하지 않는다(허용 목록, 설계 6장).
 */
@Component
class RouteAccessFilter(
    routes: List<DeclaredRoutes>,
    private val authenticator: Authenticator,
    private val errorResponder: ErrorResponder,
) : CoWebFilter() {

    private val rules: List<CompiledRule> = routes.flatMap { it.rules }.map(::CompiledRule)

    override suspend fun filter(exchange: ServerWebExchange, chain: CoWebFilterChain) {
        val rule = findRule(exchange)
            ?: return errorResponder.write(exchange, GatewayError.RouteNotFound)

        when (val access = rule.access) {
            Access.Public -> chain.filter(exchange)
            is Access.Roles -> filterByRole(exchange, chain, access)
        }
    }

    private suspend fun filterByRole(exchange: ServerWebExchange, chain: CoWebFilterChain, access: Access.Roles) {
        val principal = when (val result = authenticator.authenticate(exchange.request.headers)) {
            is AuthResult.Rejected -> return errorResponder.write(exchange, GatewayError.AuthenticationFailed(result.reason))
            is AuthResult.Authenticated -> result.principal
        }
        if (principal.role !in access.roles) {
            return errorResponder.write(exchange, GatewayError.AuthenticationFailed(roleDenied(exchange, principal)))
        }
        exchange.attributes[Principal.ATTRIBUTE] = principal
        chain.filter(exchange)
    }

    private fun findRule(exchange: ServerWebExchange): RouteRule? {
        val method = exchange.request.method.name()
        val path = exchange.request.path.pathWithinApplication()
        return rules.firstOrNull { it.rule.method.name == method && it.pattern.matches(path) }?.rule
    }

    // litellm route_checks.py 의 관리 라우트 거부 메시지와 같은 모양
    private fun roleDenied(exchange: ServerWebExchange, principal: Principal): String =
        "Only proxy admin can be used to generate, delete, update info for new keys/users/teams. " +
            "Route=${exchange.request.path.value()}. Your role=${principal.role.value}."

    private class CompiledRule(val rule: RouteRule) {
        val pattern: PathPattern = PathPatternParser.defaultInstance.parse(rule.path)
    }
}
