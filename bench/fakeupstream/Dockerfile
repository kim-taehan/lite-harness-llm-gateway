# run.sh 가 .build/ 에 linux 용 정적 바이너리를 만든다
FROM scratch
ARG TARGETARCH
COPY .build/fakeupstream-linux-${TARGETARCH} /fakeupstream
USER 65534:65534
EXPOSE 9100
ENTRYPOINT ["/fakeupstream"]
