import { NavLink, Outlet } from 'react-router-dom';

const ADMIN_NAV = [
  { to: '/admin/users', label: 'Users' },
  { to: '/admin/roles', label: 'Roles' },
  { to: '/admin/api-keys', label: 'API Keys' },
  { to: '/admin/enrollment-tokens', label: 'Enrollment Tokens' },
  { to: '/admin/alerts', label: 'Alert Rules' },
  { to: '/admin/tenant', label: 'Tenant Settings' },
  { to: '/admin/audit', label: 'Audit Log' },
];

export default function AdminLayout() {
  return (
    <div>
      <h2>Administration</h2>
      <div className="sub-nav">
        {ADMIN_NAV.map((item) => (
          <NavLink key={item.to} to={item.to} className={({ isActive }) => isActive ? 'active' : ''}>
            {item.label}
          </NavLink>
        ))}
      </div>
      <Outlet />
    </div>
  );
}
