# Docker-Self-Updater

This is a simple docker container that will update another container or simply re-create it. It is used by cosmos-server to update itself.

It expects 2 environment variables:

- `CONTAINER_NAME`: The name of the container to update
- `ACTION`: udpate or recreate