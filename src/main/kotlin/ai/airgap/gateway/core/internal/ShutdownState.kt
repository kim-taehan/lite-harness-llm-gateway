package ai.airgap.gateway.core.internal

import org.springframework.context.SmartLifecycle
import org.springframework.stereotype.Component
import java.util.concurrent.atomic.AtomicBoolean

/**
 * 종료가 시작됐는지 알린다. 기본 phase 라 웹 서버의 정상 종료보다 먼저 멈춰서,
 * 남은 요청을 처리하는 동안 liveness 가 503 을 줄 수 있다(litellm 과 같은 동작).
 */
@Component
class ShutdownState : SmartLifecycle {
    private val running = AtomicBoolean(false)
    private val shuttingDown = AtomicBoolean(false)

    fun isShuttingDown(): Boolean = shuttingDown.get()

    override fun start() {
        running.set(true)
    }

    override fun stop() {
        shuttingDown.set(true)
        running.set(false)
    }

    override fun isRunning(): Boolean = running.get()
}
