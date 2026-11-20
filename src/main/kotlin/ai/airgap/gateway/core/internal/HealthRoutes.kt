package ai.airgap.gateway.core.internal

import org.springframework.context.annotation.Bean
import org.springframework.context.annotation.Configuration
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.web.reactive.function.server.ServerRequest
import org.springframework.web.reactive.function.server.ServerResponse
import org.springframework.web.reactive.function.server.bodyValueAndAwait
import org.springframework.web.reactive.function.server.coRouter

@Configuration(proxyBeanMethods = false)
class HealthRoutes(private val shutdownState: ShutdownState) {

    @Bean
    fun healthRouter() = coRouter {
        // liveliness 는 litellm 의 옛 이름이다. 클라이언트·프로브 호환을 위해 둘 다 받는다
        GET("/health/liveliness", ::liveness)
        GET("/health/liveness", ::liveness)
    }

    private suspend fun liveness(@Suppress("UNUSED_PARAMETER") request: ServerRequest): ServerResponse =
        if (shutdownState.isShuttingDown()) {
            ServerResponse.status(HttpStatus.SERVICE_UNAVAILABLE)
                .contentType(MediaType.APPLICATION_JSON)
                .bodyValueAndAwait(SHUTTING_DOWN_BODY)
        } else {
            ServerResponse.ok()
                .contentType(MediaType.APPLICATION_JSON)
                .bodyValueAndAwait(ALIVE_BODY)
        }

    private companion object {
        // litellm 은 문자열 하나를 JSON 으로 돌려준다. 문자열 본문은 Jackson 을 거치지 않으므로 따옴표까지 직접 쓴다
        const val ALIVE_BODY = "\"I'm alive!\""
        const val SHUTTING_DOWN_BODY = """{"status":"shutting_down"}"""
    }
}
