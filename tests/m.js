const ACCESS_TOKEN =
  "EAAO3NrL0YfkBRzVtEW6K4uLi8sZCOZBPTwfvO4ZCY7j7EFMUNgrvWAZAHaYiZAQkOntTDGIlIevAzX2ZA1W4I0EiWMtTtZCZASwJ1qD1OYmzN9pbJ6RnlplfI02GrfMjgOFhEWaglXHnA7tSzFCJmIdBCWZBxMW5YRL3BHUuUzE7m0qRzcgPMVZBHpmKj5rZAj14Jc8eFdNowZAqaonB0ckertkPQmOtZBJpLs7ZBLMN0OeB0Dcdu0p8yDCBVbd9MXqmelszJTMc7GkYn8lGZBKOCjfVLZCfpivnbZAWrN4KvPOJKGCOaffuLZBAAPggImYEYxYW3EcXbwfCxIqhMhcaUZD";
const AD_ACCOUNT_ID = "1497247515390486";

async function getCampaigns() {
  const url = `https://graph.facebook.com/v23.0/act_${AD_ACCOUNT_ID}/campaigns?fields=id,name,status,objective`;

  try {
    const res = await fetch(url, {
      headers: {
        Authorization: `Bearer ${ACCESS_TOKEN}`,
      },
    });

    const data = await res.json();

    console.log(JSON.stringify(data, null, 2));
  } catch (err) {
    console.error(err);
  }
}

getCampaigns();
