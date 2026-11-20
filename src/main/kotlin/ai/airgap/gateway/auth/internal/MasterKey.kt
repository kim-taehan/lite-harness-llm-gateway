package ai.airgap.gateway.auth.internal

import org.springframework.boot.context.properties.ConfigurationProperties
import java.security.MessageDigest

/** 마스터 키가 없으면 기동하지 않는다(litellm 은 override 로 풀 수 있지만 우리는 막는다. 설계 6장) */
@ConfigurationProperties("gateway")
class MasterKeyProperties(masterKey: String?) {
    val masterKey: MasterKey = MasterKey(
        requireNotNull(masterKey?.takeIf { it.isNotBlank() }) {
            "gateway.master-key 가 없다. 환경 변수 GATEWAY_MASTER_KEY 로 넣는다"
        },
    )
}

class MasterKey(private val value: String) {
    private val bytes = value.toByteArray()

    // 비교 시간이 일치한 글자 수에 따라 달라지지 않게 한다(litellm 도 secrets.compare_digest)
    fun matches(candidate: String): Boolean = MessageDigest.isEqual(bytes, candidate.toByteArray())

    override fun toString(): String = "MasterKey(****)"
}
