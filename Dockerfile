FROM moikot/golang-dep:master as build-env

ARG APP_FOLDER=/go/src/github.com/appf/controller-psi-light/

ADD . ${APP_FOLDER}
WORKDIR ${APP_FOLDER}

RUN dep ensure -vendor-only

# Compile independent executable
RUN CGO_ENABLED=0 GOOS=linux go build -a -o /bin/main .

FROM alpine:latest
RUN apk add --no-cache tzdata

COPY --from=build-env /bin/main /

ENTRYPOINT ["/main"]