FROM debian:bullseye-slim
ARG BIN
WORKDIR /root/
RUN apt-get update
RUN apt-get install -y ca-certificates

# make cgo happy, see https://github.com/mattn/go-sqlite3/issues/855#issuecomment-1496136603
RUN apt-get install -y build-essential
COPY $BIN /root
