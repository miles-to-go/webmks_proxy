FROM golang:1.18.2 as base

WORKDIR /app

COPY ./go.mod ./
COPY ./go.sum ./
RUN go mod download

COPY ./*.go ./
RUN go build

COPY static/ static/
COPY templates/ templates/

EXPOSE 8081

CMD [ "/app/app" ]
