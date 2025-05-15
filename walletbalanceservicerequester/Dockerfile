FROM node:20-slim

WORKDIR /app
COPY package*.json ./
RUN npm install
COPY balance-puller.js .

EXPOSE 8080
CMD ["node", "balance-puller.js"]