const BASE_URL = "http://localhost:3000";

let refreshCookie = "";
let accessToken = "";

async function register() {
  console.log("\n===== REGISTER =====");

  const res = await fetch(`${BASE_URL}/auth/register`, {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
    },
    body: JSON.stringify({
      username: "john",
      email: "john@example.com",
      password: "password123",
    }),
  });

  console.log("Status:", res.status);
  console.log(await res.text());
}

async function login() {
  console.log("\n===== LOGIN =====");

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

  console.log("Status:", res.status);

  const body = await res.json();
  console.log(body);

  accessToken = body.access_token;

  const cookies = res.headers.get("set-cookie");

  if (cookies) {
    refreshCookie = cookies.split(";")[0];
    console.log("Refresh Cookie:", refreshCookie);
  }
}

async function refresh() {
  console.log("\n===== REFRESH =====");

  const res = await fetch(`${BASE_URL}/auth/refresh`, {
    method: "POST",
    headers: {
      Cookie: refreshCookie,
    },
  });

  console.log("Status:", res.status);

  const body = await res.json();
  console.log(body);

  accessToken = body.access_token;
}

async function me() {
  console.log("\n===== ME =====");

  const res = await fetch(`${BASE_URL}/me`, {
    headers: {
      Authorization: `Bearer ${accessToken}`,
    },
  });

  console.log("Status:", res.status);
  console.log(await res.text());
}

(async () => {
  await register();
  await login();
  await refresh();
  await me();
})();
