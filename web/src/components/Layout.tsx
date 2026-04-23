// web/src/components/Layout.tsx
import { Outlet, NavLink } from 'react-router-dom'

const navItems = [
  { to: '/dashboard', label: 'Dashboard' },
  { to: '/runs', label: 'Runs' },
  { to: '/repos', label: 'Repos' },
  { to: '/secrets', label: 'Secrets' },
  { to: '/providers', label: 'Providers' },
  { to: '/users', label: 'Users' },
  { to: '/audit', label: 'Audit' },
]

export default function Layout() {
  return (
    <div className="flex h-screen bg-gray-950 text-gray-100">
      <nav className="w-48 shrink-0 border-r border-gray-800 p-4 flex flex-col gap-1">
        <div className="text-lg font-semibold text-white mb-4">CronFoundry</div>
        {navItems.map(({ to, label }) => (
          <NavLink
            key={to}
            to={to}
            className={({ isActive }) =>
              `px-3 py-2 rounded text-sm ${isActive ? 'bg-gray-800 text-white' : 'text-gray-400 hover:text-white hover:bg-gray-800'}`
            }
          >
            {label}
          </NavLink>
        ))}
        <div className="mt-auto">
          <a
            href="/oauth/logout"
            className="px-3 py-2 rounded text-sm text-gray-500 hover:text-white hover:bg-gray-800 block"
          >
            Sign out
          </a>
        </div>
      </nav>
      <main className="flex-1 overflow-auto p-6">
        <Outlet />
      </main>
    </div>
  )
}
