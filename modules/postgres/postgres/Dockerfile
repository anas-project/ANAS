ARG ANAS_IMAGE_REGISTRY=ghcr.io/anas-project
FROM ${ANAS_IMAGE_REGISTRY}/anas-mirror-postgres:18.4-alpine AS extensions

# Fixed source and portable CPU flags for both published platforms. Network and
# compilation occur only in the image build, never in provision or startup.
ADD --checksum=sha256:69f4019389af05dc1c9548deb8628e62878e6e207c03907f2b8af2016472cdaa https://github.com/pgvector/pgvector/archive/refs/tags/v0.8.2.tar.gz /tmp/vector.tar.gz
RUN apk add --no-cache build-base \
    && mkdir /tmp/vector \
    && tar -xzf /tmp/vector.tar.gz --strip-components=1 -C /tmp/vector \
    && make -C /tmp/vector OPTFLAGS="" with_llvm=no \
    && make -C /tmp/vector install with_llvm=no

FROM ${ANAS_IMAGE_REGISTRY}/anas-mirror-postgres:18.4-alpine
COPY --from=extensions /usr/local/lib/postgresql/vector.so /usr/local/lib/postgresql/vector.so
COPY --from=extensions /usr/local/share/postgresql/extension/vector* /usr/local/share/postgresql/extension/
COPY --from=extensions /tmp/vector/LICENSE /usr/share/doc/pgvector/LICENSE
COPY entrypoint.sh /usr/local/bin/anas-postgres-entrypoint
COPY extensions.sh /usr/local/bin/anas-postgres-extensions
RUN chmod 755 /usr/local/bin/anas-postgres-entrypoint /usr/local/bin/anas-postgres-extensions
ENTRYPOINT ["/usr/local/bin/anas-postgres-entrypoint"]
CMD ["postgres", "-c", "password_encryption=scram-sha-256", "-c", "shared_preload_libraries="]
