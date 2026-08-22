import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { BrowserRouter, Route, Routes } from "react-router-dom";
import { AppShell } from "./app/AppShell";
import { ProfileGate } from "./features/identity/ui/ProfileGate";
import { pageRoutes } from "./app/routes";
// Before anything renders, so the first paint is already in the right
// language. Imported for its side effect — the module reads the saved choice
// and initialises i18next at load.
import "./shared/i18n";
import "./index.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, refetchOnWindowFocus: false },
  },
});

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        {/* Asks who is watching before anything is fetched under the wrong
            name — but only once a second person exists. See ProfileGate. */}
        <ProfileGate>
          <Routes>
            {/* The page list lives in one module because AppShell renders it a
              second time: on a phone the watch screen is a layer over the page
              you came from, and that page has to keep being drawn underneath. */}
            <Route element={<AppShell />}>{pageRoutes}</Route>
          </Routes>
        </ProfileGate>
      </BrowserRouter>
    </QueryClientProvider>
  </StrictMode>,
);
