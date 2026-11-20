package ai.airgap.gateway.auth.internal

import ai.airgap.gateway.auth.Principal
import ai.airgap.gateway.core.Access
import ai.airgap.gateway.core.ErrorResponder
import ai.airgap.gateway.core.Role
import ai.airgap.gateway.core.declaredRoutes
import org.junit.jupiter.api.Test
import org.springframework.test.json.JsonCompareMode
import org.springframework.test.web.reactive.server.WebTestClient
import org.springframework.web.reactive.function.server.ServerResponse
import org.springframework.web.reactive.function.server.bodyValueAndAwait
import tools.jackson.databind.json.JsonMapper

class RouteAccessFilterTest {

    private val routes = declaredRoutes {
        GET("/open", Access.Public) { ServerResponse.ok().bodyValueAndAwait("open") }
        GET("/admin", Access.roles(Role.PROXY_ADMIN)) {
            ServerResponse.ok().bodyValueAndAwait(Principal.of(it).userId)
        }
        GET("/users-only", Access.roles(Role.INTERNAL_USER)) { ServerResponse.ok().bodyValueAndAwait("x") }
    }

    private val client = WebTestClient.bindToRouterFunction(routes)
        .webFilter<WebTestClient.RouterFunctionSpec>(
            RouteAccessFilter(
                listOf(routes),
                Authenticator(MasterKeyProperties(MASTER_KEY)),
                ErrorResponder(JsonMapper.builder().build()),
            ),
        )
        .build()

    @Test
    fun `공개 라우트는 키 없이 열린다`() {
        client.get().uri("/open").exchange().expectStatus().isOk
    }

    @Test
    fun `선언하지 않은 경로는 litellm 과 같은 404 를 준다`() {
        client.get().uri("/nowhere").exchange()
            .expectStatus().isNotFound
            .expectBody().json("""{"detail":"Not Found"}""", JsonCompareMode.STRICT)
    }

    @Test
    fun `키가 없으면 litellm 과 같은 401 본문을 준다`() {
        client.get().uri("/admin").exchange()
            .expectStatus().isUnauthorized
            .expectBody().json(
                """{"error":{"message":"Authentication Error, No api key passed in.","type":"auth_error","param":"None","code":"401"}}""",
                JsonCompareMode.STRICT,
            )
    }

    @Test
    fun `틀린 키는 401 이다`() {
        client.get().uri("/admin").header("Authorization", "Bearer sk-wrong").exchange()
            .expectStatus().isUnauthorized
            .expectBody().jsonPath("$.error.message").isEqualTo("Authentication Error, Invalid proxy server token passed.")
    }

    @Test
    fun `마스터 키는 Bearer 와 x-api-key 로 받고 proxy_admin 신원이 된다`() {
        client.get().uri("/admin").header("Authorization", "Bearer $MASTER_KEY").exchange()
            .expectStatus().isOk.expectBody(String::class.java).isEqualTo("default_user_id")
        client.get().uri("/admin").header("x-api-key", MASTER_KEY).exchange()
            .expectStatus().isOk
    }

    @Test
    fun `허용되지 않은 역할은 litellm 과 같이 401 로 거부한다`() {
        client.get().uri("/users-only").header("Authorization", "Bearer $MASTER_KEY").exchange()
            .expectStatus().isUnauthorized
            .expectBody().jsonPath("$.error.type").isEqualTo("auth_error")
    }

    private companion object {
        const val MASTER_KEY = "sk-test-master"
    }
}
