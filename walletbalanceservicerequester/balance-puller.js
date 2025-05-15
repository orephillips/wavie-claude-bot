const express = require('express');
const { BigQuery } = require('@google-cloud/bigquery');
const { SecretManagerServiceClient } = require('@google-cloud/secret-manager');
const axios = require('axios');
const { RateLimiterMemory } = require('rate-limiter-flexible');
const dotenv = require('dotenv');

dotenv.config();

const app = express();
const PORT = process.env.PORT || 8080;
const bigquery = new BigQuery();
const secretManager = new SecretManagerServiceClient();

// Configuration
const PROJECT_ID = process.env.GCP_PROJECT || 'bitwave-customers';
const INPUT_TABLE = 'bitwave-customers.balance_puller_service.balance_pull_requests';
const OUTPUT_TABLE = 'bitwave-customers.balance_puller_service.balance_pull_results';
const API_BASE_URL = 'https://walletbalanceservice-455488113475.us-central1.run.app';
const BATCH_SIZE = 50;
const GLOBAL_RATE_LIMITER = new RateLimiterMemory({ points: 20, duration: 1 }); // 20 calls/sec
let API_KEY = null;

// Middleware
app.use(express.json());

// Fetch API key
async function fetchApiKey() {
  if (!API_KEY) {
    try {
      const [version] = await secretManager.accessSecretVersion({
        name: `projects/${PROJECT_ID}/secrets/bitwave-api-key/versions/latest`
      });
      API_KEY = version.payload.data.toString('utf8');
    } catch (error) {
      console.error('Error fetching API key:', error);
      throw error;
    }
  }
  return API_KEY;
}

// Fetch balance
async function fetchBalance(chain, address, tokenAddress, balanceType) {
  try {
    await GLOBAL_RATE_LIMITER.consume(`${chain}:${address}`);
    const apiKey = await fetchApiKey();
    const response = await axios.get(`${API_BASE_URL}/api/v1/chains/${chain}/addresses/${address}/balance`, {
      params: { apiKey, type: balanceType, contractaddress: tokenAddress }
    });
    if (!response.data.success) throw new Error(response.data.errors[0]);
    return response.data.data;
  } catch (error) {
    if (error.response && error.response.status === 429) {
      console.warn(`Rate limit hit for ${chain}/${address}, retrying after 1s`);
      await new Promise(resolve => setTimeout(resolve, 1000));
      return fetchBalance(chain, address, tokenAddress, balanceType);
    }
    console.error(`Error fetching balance for ${chain}/${address}: ${error.message}`);
    return null;
  }
}

// Process batch
async function processBatch(rows) {
  const results = [];
  const promises = rows.map(row => fetchBalance(
    row.Chain,
    row.WalletAddress,
    row.TokenAddress,
    row.Type
  ));

  const responses = await Promise.all(promises);

  for (let i = 0; i < rows.length; i++) {
    const row = rows[i];
    const response = responses[i];
    if (!response) continue;

    results.push({
      ClientName: row.ClientName,
      RequestedBy: row.RequestedBy,
      Chain: row.Chain,
      WalletAddress: row.WalletAddress,
      TokenAddress: row.TokenAddress,
      Ticker: response.Ticker,
      Amount: parseFloat(response.Amount),
      Timestamp: new Date(parseInt(response.TimestampSEC) * 1000).toISOString(),
      RunTimestamp: new Date().toISOString(),
      RawResponse: JSON.stringify(response.RawMetadata)
    });
  }

  return results;
}

// Write to BigQuery
async function writeToBigQuery(results) {
  if (!results.length) {
    console.log('No results to write to BigQuery');
    return;
  }
  try {
    await bigquery
      .dataset('balance_puller_service')
      .table('balance_pull_results')
      .insert(results);
    console.log(`Wrote ${results.length} rows to BigQuery`);
  } catch (error) {
    console.error('Error writing to BigQuery:', error);
    throw error;
  }
}

// HTTP endpoint for Cloud Scheduler
app.post('/pull', async (req, res) => {
  try {
    console.log('Starting balance puller service');
    const query = `
      SELECT ClientName, RequestedBy, Chain, WalletAddress, TokenAddress, Type
      FROM \`${INPUT_TABLE}\`
      WHERE processed IS NULL OR processed = FALSE
      LIMIT ${BATCH_SIZE}
    `;
    const [rows] = await bigquery.query({ query });
    if (!rows.length) {
      console.log('No new balance pull requests found');
      return res.status(200).json({ message: 'No new requests' });
    }
    const results = await processBatch(rows);
    await writeToBigQuery(results);
    const processedIds = rows.map(row => row.WalletAddress);
    if (processedIds.length) {
      const updateQuery = `
        UPDATE \`${INPUT_TABLE}\`
        SET processed = TRUE
        WHERE WalletAddress IN UNNEST(@ids)
      `;
      await bigquery.query({
        query: updateQuery,
        params: { ids: processedIds }
      });
    }
    console.log(`Processed ${results.length} balance requests`);
    res.status(200).json({ message: `Processed ${results.length} requests` });
  } catch (error) {
    console.error('Error in balance puller:', error);
    res.status(500).json({ error: error.message });
  }
});

// Health check endpoint
app.get('/health', (req, res) => {
  res.status(200).json({ status: 'healthy' });
});

app.listen(PORT, () => {
  console.log(`Balance Puller Service running on port ${PORT}`);
});