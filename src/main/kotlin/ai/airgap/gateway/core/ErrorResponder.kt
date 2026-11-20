package ai.airgap.gateway.core

import kotlinx.coroutines.reactor.awaitSingleOrNull
import org.springframework.http.HttpStatus
import org.springframework.http.MediaType
import org.springframework.stereotype.Component
import org.springframework.web.reactive.function.server.ServerResponse
import org.springframework.web.reactive.function.server.bodyValueAndAwait
import org.springframework.web.server.ServerWebExchange
import reactor.core.publisher.Mono
import tools.jackson.databind.json.JsonMapper

/** [GatewayError] 를 litellm 과 같은 HTTP 상태·본문으로 바꾼다 */
@Component
class ErrorResponder(private val jsonMapper: JsonMapper) {

    /** 핸들러에서 쓴다 */
    suspend fun respond(error: GatewayError): ServerResponse {
        val rendered = render(error)
        return ServerResponse.status(rendered.status)
            .contentType(MediaType.APPLICATION_JSON)
            .bodyValueAndAwait(rendered.body)
    }

    /** 핸들러에 닿기 전(필터)에서 쓴다 */
    suspend fun write(exchange: ServerWebExchange, error: GatewayError) {
        val rendered = render(error)
        val response = exchange.response
        response.statusCode = rendered.status
        response.headers.contentType = MediaType.APPLICATION_JSON
        val buffer = response.bufferFactory().wrap(jsonMapper.writeValueAsBytes(rendered.body))
        response.writeWith(Mono.just(buffer)).awaitSingleOrNull()
    }

    private fun render(error: GatewayError): Rendered = when (error) {
        is GatewayError.AuthenticationFailed -> openAiError(
            HttpStatus.UNAUTHORIZED,
            message = "Authentication Error, ${error.reason}",
            type = "auth_error",
        )
        GatewayError.RouteNotFound -> Rendered(HttpStatus.NOT_FOUND, mapOf("detail" to "Not Found"))
    }

    // litellm ProxyException.to_dict 와 같다. param 은 문자열 "None", code 는 상태 코드 문자열이다
    private fun openAiError(status: HttpStatus, message: String, type: String): Rendered =
        Rendered(
            status,
            mapOf(
                "error" to mapOf(
                    "message" to message,
                    "type" to type,
                    "param" to "None",
                    "code" to status.value().toString(),
                ),
            ),
        )

    private data class Rendered(val status: HttpStatus, val body: Map<String, Any>)
}
