FROM alpine:3.22

COPY --chmod=0555 sync-contract.sh /usr/local/bin/git-mirror-sync

USER 65532:65532
ENTRYPOINT ["/usr/local/bin/git-mirror-sync"]
