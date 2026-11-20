package ai.airgap.gateway.core.internal

import org.junit.jupiter.api.Test
import org.springframework.http.MediaType
import org.springframework.test.web.reactive.server.WebTestClient

class HealthRoutesTest {

    private fun client(state: ShutdownState) =
        WebTestClient.bindToRouterFunction(HealthRoutes(state).healthRouter()).build()

    @Test
    fun `살아 있으면 litellm 과 같은 응답을 준다`() {
        val client = client(ShutdownState().apply { start() })

        listOf("/health/liveliness", "/health/liveness").forEach { path ->
            client.get().uri(path).exchange()
                .expectStatus().isOk
                .expectHeader().contentTypeCompatibleWith(MediaType.APPLICATION_JSON)
                .expectBody().json("\"I'm alive!\"")
        }
    }

    @Test
    fun `종료가 시작되면 503 을 준다`() {
        val state = ShutdownState().apply { start() }
        state.stop()

        client(state).get().uri("/health/liveness").exchange()
            .expectStatus().isEqualTo(503)
            .expectBody().json("""{"status":"shutting_down"}""")
    }
}
