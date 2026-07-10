FROM public.ecr.aws/docker/library/alpine:latest
RUN apk update && apk add tzdata && rm  -rf /tmp/* /var/cache/apk/*
WORKDIR /app
COPY ./docport /app/docport
ENTRYPOINT ["/app/docport"]
