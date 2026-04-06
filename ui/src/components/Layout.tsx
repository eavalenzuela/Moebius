import { NavLink, Outlet } from 'react-router-dom';
import { useAuth } from '../auth/useAuth';

const NAV_ITEMS = [
  { to: '/devices', label: 'Devices' },
  { to: '/jobs', label: 'Jobs' },
  { to: '/scheduled', label: 'Schedules' },
  { to: '/files', label: 'Files' },
  { to: '/admin', label: 'Admin' },
];

export default function Layout() {
  const { logout } = useAuth();

  return (
    <div className="app-layout">
      <nav className="sidebar">
        <div className="sidebar-brand">Moebius</div>
        <ul>
          {NAV_ITEMS.map((item) => (
            <li key={item.to}>
              <NavLink to={item.to} className={({ isActive }) => isActive ? 'active' : ''}>
                {item.label}
              </NavLink>
            </li>
          ))}
        </ul>
        <button className="logout-btn" onClick={logout}>Sign Out</button>
      </nav>
      <main className="main-content">
        <Outlet />
      </main>
    </div>
  );
}
