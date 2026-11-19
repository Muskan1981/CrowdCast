# CrowdCast frontend - multi-stage build.
# Stage 1 builds the static bundle with Node. Stage 2 serves it with nginx.

ARG NODE_VERSION=22
FROM node:${NODE_VERSION}-alpine AS build
WORKDIR /app

# Install dependencies first so this layer is cached until the lock file changes.
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

COPY . .
# Build-time settings. Empty VITE_API_URL means same origin, which is how nginx serves it.
# VITE_ADMIN_KEY is for the demo's chaos buttons only: it ends up in the public bundle.
ARG VITE_API_URL=""
ARG VITE_ADMIN_KEY=""
ARG VITE_USE_MOCKS="false"
RUN npm run build

FROM nginx:1.27-alpine

COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html

EXPOSE 80

HEALTHCHECK --interval=10s --timeout=3s --retries=3 \
  CMD wget -qO- http://127.0.0.1/ >/dev/null || exit 1
