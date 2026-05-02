import './index.css'
import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom'
import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import Layout from './components/Layout'
import Dashboard from './pages/Dashboard'
import Runs from './pages/Runs'
import Repos from './pages/Repos'
import Secrets from './pages/Secrets'
import Users from './pages/Users'
import Audit from './pages/Audit'
import Login from './pages/Login'
import Providers from './pages/Providers'

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 10_000, retry: 1 },
  },
})

/**
 * Routes follow the new IA: Operate (overview/jobs) at top level,
 * Settings as a grouped namespace. Old routes redirect to their new
 * locations so existing bookmarks/links keep working.
 *
 * PR1 scope: shell + nav only. The existing page components are
 * mounted at their new paths unchanged — visual refresh of those
 * pages happens in PR2.
 */
ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <QueryClientProvider client={queryClient}>
      <BrowserRouter>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route element={<Layout />}>
            {/* Default landing → Overview */}
            <Route index element={<Navigate to="/overview" replace />} />

            {/* OPERATE — Overview & Jobs replace Dashboard.
                Until PR2 builds the new pages, /overview and /jobs
                both render the legacy Dashboard component. */}
            <Route path="/overview" element={<Dashboard />} />
            <Route path="/jobs" element={<Dashboard />} />

            {/* Runs page kept reachable for deep links from Overview's
                activity card and from Job detail; not a top-level nav. */}
            <Route path="/runs" element={<Runs />} />

            {/* SETTINGS — grouped namespace.
                Bare /settings sends users to the default sub-page so
                the route isn't a dead end. */}
            <Route path="/settings" element={<Navigate to="/settings/repos" replace />} />
            <Route path="/settings/repos" element={<Repos />} />
            <Route path="/settings/secrets" element={<Secrets />} />
            <Route path="/settings/providers" element={<Providers />} />
            <Route path="/settings/users" element={<Users />} />
            <Route path="/settings/audit" element={<Audit />} />

            {/* Legacy redirects — preserve existing bookmarks */}
            <Route path="/dashboard" element={<Navigate to="/overview" replace />} />
            <Route path="/repos" element={<Navigate to="/settings/repos" replace />} />
            <Route path="/secrets" element={<Navigate to="/settings/secrets" replace />} />
            <Route path="/providers" element={<Navigate to="/settings/providers" replace />} />
            <Route path="/users" element={<Navigate to="/settings/users" replace />} />
            <Route path="/audit" element={<Navigate to="/settings/audit" replace />} />
          </Route>
        </Routes>
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>,
)
