package ai.airgap.gateway

import org.junit.jupiter.api.Test
import org.springframework.modulith.core.ApplicationModules

class ModuleStructureTest {

    @Test
    fun `모듈 경계를 지킨다`() {
        ApplicationModules.of(LlmGatewayApplication::class.java).verify()
    }
}
