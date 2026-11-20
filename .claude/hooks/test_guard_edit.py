#!/usr/bin/env python3
"""guard_edit.py 동작 테스트. 실행: python3 .claude/hooks/test_guard_edit.py"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

HOOK = os.path.join(os.path.dirname(os.path.abspath(__file__)), "guard_edit.py")
PROJECT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
MAIN = "src/main/kotlin/ai/airgap/gateway/"


def run(tool_name, rel, **tool_input):
    tool_input["file_path"] = os.path.join(PROJECT, rel)
    event = {"tool_name": tool_name, "tool_input": tool_input, "cwd": PROJECT}
    result = subprocess.run(
        [sys.executable, HOOK], input=json.dumps(event), capture_output=True, text=True,
        env={**os.environ, "CLAUDE_PROJECT_DIR": PROJECT},
    )
    return result.returncode, result.stderr


def kt(package, body=""):
    return f"package ai.airgap.gateway.{package}\n\n{body}\n"


class GuardEditTest(unittest.TestCase):

    def assertBlocked(self, result, phrase):
        code, err = result
        self.assertEqual(2, code, err)
        self.assertIn(phrase, err)

    def assertAllowed(self, result):
        code, err = result
        self.assertEqual(0, code, err)

    # 모듈 경계
    def test_다른_모듈의_internal_참조를_막는다(self):
        src = kt("admin", "import ai.airgap.gateway.auth.internal.Authenticator")
        self.assertBlocked(run("Write", MAIN + "admin/X.kt", content=src), "auth.internal")

    def test_완전한_이름으로_internal_을_불러도_막는다(self):
        src = kt("admin", "class X(val a: ai.airgap.gateway.auth.internal.Authenticator)")
        self.assertBlocked(run("Write", MAIN + "admin/X.kt", content=src), "auth.internal")

    def test_거꾸로_된_의존을_막는다(self):
        src = kt("core", "import ai.airgap.gateway.auth.Principal")
        self.assertBlocked(run("Write", MAIN + "core/X.kt", content=src), "`core` → `auth`")

    def test_auth_와_limits_는_서로_기대지_않는다(self):
        src = kt("auth", "import ai.airgap.gateway.limits.LimitGate")
        self.assertBlocked(run("Write", MAIN + "auth/X.kt", content=src), "`auth` → `limits`")

    def test_허용된_방향의_공개_API_는_통과한다(self):
        src = kt("admin", "import ai.airgap.gateway.auth.Principal\nimport ai.airgap.gateway.core.Role")
        self.assertAllowed(run("Write", MAIN + "admin/X.kt", content=src))

    def test_같은_모듈의_internal_은_통과한다(self):
        src = kt("auth", "import ai.airgap.gateway.auth.internal.Authenticator")
        self.assertAllowed(run("Write", MAIN + "auth/X.kt", content=src))

    def test_설계에_없는_모듈을_막는다(self):
        self.assertBlocked(run("Write", MAIN + "billing/X.kt", content=kt("billing")), "설계에 없다")

    def test_뭉뚱그린_패키지_이름을_막는다(self):
        self.assertBlocked(run("Write", MAIN + "core/util/X.kt", content=kt("core.util")), "`util`")

    def test_패키지와_경로가_다르면_막는다(self):
        self.assertBlocked(run("Write", MAIN + "auth/X.kt", content=kt("core")), "파일 위치")

    # 코틀린 관례
    def test_느낌표_두개를_막는다(self):
        self.assertBlocked(run("Write", MAIN + "core/X.kt", content=kt("core", "val a = b!!")), "`!!`")

    def test_문자열과_주석_안의_패턴은_무시한다(self):
        src = kt("core", '// a!! runBlocking\nval s = "x!! GlobalScope"\nval t = """\nThread.sleep\n"""')
        self.assertAllowed(run("Write", MAIN + "core/X.kt", content=src))

    def test_GlobalScope_runBlocking_Thread_sleep_을_막는다(self):
        self.assertBlocked(run("Write", MAIN + "core/X.kt", content=kt("core", "GlobalScope.launch {}")), "GlobalScope")
        self.assertBlocked(run("Write", MAIN + "core/X.kt", content=kt("core", "runBlocking {}")), "runBlocking")
        self.assertBlocked(run("Write", MAIN + "core/X.kt", content=kt("core", "Thread.sleep(1)")), "Thread.sleep")

    def test_테스트_코드의_runBlocking_은_통과한다(self):
        rel = "src/test/kotlin/ai/airgap/gateway/core/XTest.kt"
        self.assertAllowed(run("Write", rel, content=kt("core", "runBlocking {}")))

    def test_필드_주입을_막는다(self):
        src = kt("core", "class X { @Autowired lateinit var y: Y }")
        self.assertBlocked(run("Write", MAIN + "core/X.kt", content=src), "@Autowired")

    def test_JDBC_를_막는다(self):
        src = kt("auth.internal", "import org.springframework.jdbc.core.JdbcTemplate\nclass R(val t: JdbcTemplate)")
        self.assertBlocked(run("Write", MAIN + "auth/internal/R.kt", content=src), "R2DBC")

    def test_취소를_삼키는_catch_를_막는다(self):
        src = kt("core", "fun f() { try { g() } catch (e: Exception) { log(e) } }")
        self.assertBlocked(run("Write", MAIN + "core/X.kt", content=src), "CancellationException")

    def test_취소를_다시_던지면_통과한다(self):
        src = kt("core", "fun f() { try { g() } catch (e: CancellationException) { throw e } catch (e: Exception) { log(e) } }")
        self.assertAllowed(run("Write", MAIN + "core/X.kt", content=src))

    def test_외부_주소를_막고_내부_주소는_통과한다(self):
        self.assertBlocked(run("Write", MAIN + "core/X.kt", content=kt("core", 'val u = "https://api.openai.com/v1"')), "외부 주소")
        self.assertAllowed(run("Write", MAIN + "core/X.kt", content=kt("core", 'val u = "http://localhost:11434"')))

    # Edit 은 적용 결과 전체로 검사한다
    def test_Edit_결과로_검사한다(self):
        with tempfile.TemporaryDirectory(dir=os.path.join(PROJECT, "src/main/kotlin/ai/airgap/gateway/core")) as d:
            rel = os.path.relpath(os.path.join(d, "X.kt"), PROJECT)
            pkg = "core." + os.path.basename(d)
            with open(os.path.join(PROJECT, rel), "w") as f:
                f.write(kt(pkg, "val a = 1"))
            self.assertBlocked(run("Edit", rel, old_string="val a = 1", new_string="val a = b!!"), "`!!`")
            self.assertAllowed(run("Edit", rel, old_string="val a = 1", new_string="val a = 2"))

    # Flyway
    def test_마이그레이션은_스키마만_바꾼다(self):
        rel = "src/main/resources/db/migration/V99__seed.sql"
        self.assertBlocked(run("Write", rel, content="INSERT INTO team VALUES (1);"), "스키마만")
        self.assertAllowed(run("Write", rel, content="CREATE TABLE t (id uuid primary key);"))

    def test_마이그레이션_파일_이름_규칙(self):
        rel = "src/main/resources/db/migration/create_team.sql"
        self.assertBlocked(run("Write", rel, content="CREATE TABLE t (id int);"), "V<번호>")

    def test_규칙_밖의_파일은_통과한다(self):
        self.assertAllowed(run("Write", "GATEWAY_DESIGN.md", content="a!! runBlocking"))


if __name__ == "__main__":
    unittest.main(verbosity=1)
