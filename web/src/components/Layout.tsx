import { NavLink, Outlet, useNavigate } from "react-router";
import { APP_VERSION } from "../config";
import { useApi } from "@/api/useApi";
import { DebugCaptureStatusBadge } from "@/features/debug-capture";

const navItems: Array<{
  to: string;
  label: string;
  icon: string;
  showCaptureStatus?: boolean;
}> = [
  { to: "/", label: "Dashboard", icon: "📊" },
  { to: "/monitor", label: "Monitor", icon: "📡" },
  { to: "/providers", label: "Providers", icon: "🔌" },
  { to: "/credentials", label: "Credentials", icon: "🔑" },
  { to: "/client-disguise", label: "Client disguise", icon: "🖥️" },
  { to: "/groups", label: "Groups", icon: "📁" },
  { to: "/routing", label: "Routing", icon: "🧭" },
  { to: "/error-detection", label: "Error Detection", icon: "🛡️" },
  { to: "/config", label: "Config", icon: "⚙️" },
  { to: "/logs", label: "Logs", icon: "📋" },
  { to: "/token-usage", label: "Token Usage", icon: "📈" },
  {
    to: "/debug-capture",
    label: "Debug Capture",
    icon: "🐞",
    showCaptureStatus: true,
  },
];

export function Layout() {
  const { clearToken } = useApi();
  const navigate = useNavigate();

  const handleLogout = () => {
    clearToken();
    navigate("/login");
  };

  return (
    <div className="min-h-screen bg-bg-secondary">
      {/* Header */}
      <header className="bg-white border-b border-border sticky top-0 z-10 shadow-sm">
        <div className="w-full max-w-[1920px] mx-auto px-4 sm:px-6 lg:px-8">
          <div className="flex items-center justify-between h-16">
            <div className="flex items-center gap-3">
              <div className="w-9 h-9 bg-linear-to-br from-primary to-indigo-500 rounded-lg flex items-center justify-center shadow-md">
                <span className="text-white text-lg">⚡</span>
              </div>
              <div>
                <h1 className="text-lg font-bold text-text-primary">
                  Switch-A
                </h1>
                <p className="text-xs text-text-muted -mt-0.5">
                  AI Provider Proxy
                </p>
              </div>
            </div>
            <div className="flex items-center gap-4">
              <div className="flex items-center gap-2 px-3 py-1.5 bg-success-light rounded-full">
                <span className="w-2 h-2 bg-success rounded-full animate-pulse"></span>
                <span className="text-xs font-medium text-emerald-700">
                  Online
                </span>
              </div>
              <button
                onClick={handleLogout}
                className="flex items-center gap-2 px-3 py-1.5 text-sm text-text-secondary hover:text-text-primary hover:bg-bg-hover rounded-lg transition-colors cursor-pointer"
              >
                <span>🚪</span>
                <span>Logout</span>
              </button>
            </div>
          </div>
        </div>
      </header>

      <div className="w-full max-w-[1920px] mx-auto px-4 sm:px-6 lg:px-8 py-6">
        <div className="flex flex-col gap-6 lg:flex-row">
          {/* Sidebar Navigation */}
          <nav
            aria-label="Primary"
            className="w-full min-w-0 lg:w-52 lg:shrink-0"
          >
            <div className="overflow-x-auto rounded-xl border border-border bg-white p-2 shadow-sm lg:overflow-visible">
              <ul className="flex min-w-max gap-1 lg:block lg:min-w-0 lg:space-y-1">
                {navItems.map((item) => (
                  <li key={item.to} className="shrink-0">
                    <NavLink
                      to={item.to}
                      end={item.to === "/"}
                      className={({ isActive }) =>
                        `flex items-center gap-3 whitespace-nowrap px-4 py-2.5 rounded-lg transition-all duration-200 ${
                          isActive
                            ? "bg-linear-to-r from-primary to-indigo-500 text-white shadow-md"
                            : "text-text-secondary hover:bg-bg-hover hover:text-text-primary"
                        }`
                      }
                    >
                      <span className="text-base" aria-hidden="true">
                        {item.icon}
                      </span>
                      <span className="font-medium text-sm">{item.label}</span>
                      {item.showCaptureStatus && <DebugCaptureStatusBadge />}
                    </NavLink>
                  </li>
                ))}
              </ul>
            </div>

            {/* Version Info */}
            <div className="mt-4 hidden rounded-xl border border-border bg-white px-4 py-3 text-center lg:block">
              <p className="text-xs text-text-muted">Version {APP_VERSION}</p>
            </div>
          </nav>

          {/* Main Content */}
          <main className="flex-1 min-w-0">
            <Outlet />
          </main>
        </div>
      </div>
    </div>
  );
}
