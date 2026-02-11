import { NavLink, Outlet } from "react-router-dom"
import { cn } from "@/lib/utils"

const tabs = [
  { to: "/settings/account", label: "Account" },
  { to: "/settings/providers", label: "Providers" },
  { to: "/settings/tools", label: "Tools" },
  { to: "/settings/preferences", label: "Preferences" },
  { to: "/settings/about", label: "About" },
]

export default function SettingsPage() {
  return (
    <div className="container mx-auto py-6">
      <h1 className="text-2xl font-bold">Settings</h1>
      <nav className="mt-4 flex gap-1 border-b">
        {tabs.map((tab) => (
          <NavLink
            key={tab.to}
            to={tab.to}
            className={({ isActive }) =>
              cn(
                "-mb-px border-b-2 px-4 py-2 text-sm font-medium transition-colors",
                isActive
                  ? "border-primary text-foreground"
                  : "border-transparent text-muted-foreground hover:text-foreground",
              )
            }
          >
            {tab.label}
          </NavLink>
        ))}
      </nav>
      <div className="mt-6">
        <Outlet />
      </div>
    </div>
  )
}
