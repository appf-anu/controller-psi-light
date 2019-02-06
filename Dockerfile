FROM alpine:latest
RUN apk add --no-cache tzdata
COPY controller-psi-light /bin
COPY run.sh /bin
VOLUME /conditions
ENTRYPOINT ["run.sh"]
