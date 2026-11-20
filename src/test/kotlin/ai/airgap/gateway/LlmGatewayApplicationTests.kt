package ai.airgap.gateway

import ai.airgap.gateway.core.DeclaredRoutes
import org.junit.jupiter.api.Test
import org.springframework.beans.factory.annotation.Autowired
import org.springframework.web.reactive.function.server.RouterFunction
import kotlin.test.assertEquals
import org.springframework.boot.test.context.SpringBootTest
import org.springframework.context.annotation.Import
import org.springframework.test.context.ActiveProfiles

@SpringBootTest
@ActiveProfiles("test")
@Import(PostgresTestConfiguration::class)
class LlmGatewayApplicationTests(@Autowired private val routers: List<RouterFunction<*>>) {

	@Test
	fun contextLoads() {
	}

	@Test
	fun `모든 라우트에 권한 선언이 있다`() {
		val undeclared = routers.filterNot { it is DeclaredRoutes }
		assertEquals(emptyList(), undeclared, "DeclaredRoutes 가 아닌 라우터는 권한 선언이 없다")
	}

}
