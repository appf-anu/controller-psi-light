FROM alpine:latest
RUN apk add --no-cache tzdata
COPY controller-psi-light /bin
VOLUME /data
ENTRYPOINT ["controller-psi-light"]
CMD ["/dev/ttyUSB0"]