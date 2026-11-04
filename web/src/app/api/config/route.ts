import { connection } from "next/server";

// Runtime configuration for the browser. NEXT_PUBLIC_* variables are frozen at
// build time, which does not work for one Docker image deployed to many
// domains, so the API URL is read from the server environment per request.
export async function GET() {
  await connection();
  return Response.json(
    { apiUrl: (process.env.HS_API_URL ?? "http://localhost:8081").replace(/\/+$/, "") },
    { headers: { "Cache-Control": "no-store" } },
  );
}
