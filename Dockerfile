FROM node:24-alpine AS build
WORKDIR /app
ARG VITE_BASE_PATH=/mail/
ENV VITE_BASE_PATH=$VITE_BASE_PATH
COPY package*.json ./
RUN npm ci
COPY . .
RUN npm run build

FROM node:24-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
COPY package*.json ./
RUN npm ci --omit=dev
COPY --from=build /app/build ./build
COPY --from=build /app/dist ./dist
RUN mkdir -p /app/data && chown -R node:node /app/data
USER node
EXPOSE 3000
CMD ["node", "build/server/index.js"]
