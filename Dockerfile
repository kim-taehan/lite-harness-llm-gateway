# 바이너리 하나만 든 이미지 — OS 패키지·셸이 없다. 먼저 ./build.sh 로 dist/ 를 만든다.
#   docker build -t llm-gateway .                         (이 컴퓨터의 아키텍처)
#   docker build --platform linux/amd64 -t llm-gateway .  (다른 아키텍처용)
# 업스트림(Ollama)·Keycloak·PostgreSQL 은 사내 평문 주소를 가정한다. https 를 쓰려면 사내 CA 인증서를 이미지에 넣어야 한다(scratch 라 기본 CA 가 없다).
FROM scratch
ARG TARGETARCH
COPY dist/llm-gateway-linux-${TARGETARCH} /llm-gateway
USER 65534:65534
EXPOSE 4000
ENTRYPOINT ["/llm-gateway"]
