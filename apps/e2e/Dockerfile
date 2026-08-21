# Playwright's own image, pinned to the exact same version as
# package.json's @playwright/test — the browsers baked into this image and
# the test runner's own version have to match, or Playwright refuses to
# launch at all.
FROM mcr.microsoft.com/playwright:v1.62.1-noble

WORKDIR /app
COPY package.json ./
RUN npm install
COPY playwright.config.ts ./
COPY tests ./tests

ENTRYPOINT ["npx", "playwright", "test"]
