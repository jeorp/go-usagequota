FROM node:22-trixie-slim

ARG FIREBASE_TOOLS_VERSION=15.30.1

RUN apt-get update \
    && apt-get install --yes --no-install-recommends openjdk-21-jre-headless \
    && rm -rf /var/lib/apt/lists/* \
    && npm install --global "firebase-tools@${FIREBASE_TOOLS_VERSION}" \
    && npm cache clean --force

ENV CI=true \
    FIREBASE_CLI_DISABLE_UPDATE_CHECK=true \
    HOME=/home/node

WORKDIR /workspace
COPY --chown=node:node firebase.json firestore.rules ./
RUN mkdir -p /home/node/.cache/firebase/emulators \
    && chown -R node:node /workspace /home/node/.cache
USER node
EXPOSE 8080
ENTRYPOINT ["firebase", "emulators:start", "--only", "firestore", "--project", "demo-go-usagequota"]
