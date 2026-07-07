require("dotenv").config();

const BASE_URL = "http://localhost:3000";

let accessToken = "";

async function login() {
  const res = await fetch(`${BASE_URL}/auth/login`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      email: "john@example.com",
      password: "password123",
    }),
  });

  if (!res.ok) {
    throw new Error(await res.text());
  }

  const body = await res.json();

  accessToken = body.access_token;

  console.log("✓ Logged in");
}

async function createProvider() {
  const res = await fetch(`${BASE_URL}/providers`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${accessToken}`,
    },
    body: JSON.stringify({
      model_name: "gemini-3.5-flash",
      api_key: process.env.GEMINI_API_KEY,
    }),
  });

  console.log("Provider Status:", res.status);
  console.log(await res.text());
}

async function generate() {
  const res = await fetch(`${BASE_URL}/ai/generate`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${accessToken}`,
    },
    body: JSON.stringify({
      model_name: "gemini-3.5-flash",
      prompt: "Say hello from Gemini.",
    }),
  });

  console.log("Generate Status:", res.status);
  console.log(await res.text());
}

(async () => {
  try {
    await login();
    await createProvider();
    await generate();
  } catch (err) {
    console.error(err);
  }
})();
