import {useEffect,useState} from 'react';
import {NavLink,Outlet,ScrollRestoration,useRouteError,useLocation,Link} from 'react-router';
import {Button} from './components/Button';
import {Icon} from './components/Icon';
import {readPreferences,savePreferences,type Preferences} from './preferences';
import {errorMessage} from './api/client';
import {useUploads} from './uploads/runtime';

export interface AppContext {preferences:Preferences;setPreferences:(p:Preferences)=>void}
export function App(){
 const viewing=useLocation().pathname.startsWith('/content/');const uploads=useUploads();const uploadCount=uploads.rows.length+uploads.preparing.length;
 const [preferences,setPreferences]=useState(readPreferences);
 useEffect(()=>{savePreferences(preferences);document.documentElement.classList.toggle('sidebar-collapsed',!preferences.sidebarOpen);},[preferences]);
 const links=[['/library','Library','library'],['/collections','Collections','folder'],['/tags','Tags','tag'],['/categories','Categories','bookmark']];
 return <><a className="skip-link" href="#workspace">Skip to content</a><Button className="ghost icon-button sidebar-toggle" label={preferences.sidebarOpen?'Collapse sidebar':'Expand sidebar'} aria-controls="sidebar" aria-expanded={preferences.sidebarOpen} onClick={()=>setPreferences({...preferences,sidebarOpen:!preferences.sidebarOpen})}><svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.7" aria-hidden="true"><path d="M4 6h16M4 12h16M4 18h16"/></svg></Button><div className="app-shell"><aside id="sidebar" className="sidebar" aria-label="Main navigation"><Link className="nav-brand" to="/library" aria-label="TagFiles"><span>TagFiles<span className="brand-dot">.</span></span></Link><nav className="nav-section nav-primary" aria-label="Library navigation">{links.map(([to,label,icon])=><NavLink key={to} to={to!} aria-current={viewing&&to==='/library'?'page':undefined} className={({isActive})=>`nav-link${isActive||viewing&&to==='/library'?' active':''}`} aria-label={label} title={label}><Icon name={icon!}/><span className="nav-label">{label}</span></NavLink>)}</nav><div className="nav-section nav-bottom"><NavLink to="/uploads" className={({isActive})=>`nav-link${isActive?' active':''}`} aria-label="Uploads" title="Uploads"><Icon name="upload"/><span className="nav-label">Uploads</span>{uploadCount>0&&<span className="nav-count" aria-label={`${uploadCount} pending uploads`}>{uploadCount}</span>}</NavLink></div></aside><main id="workspace" className="workspace" tabIndex={-1}><Outlet context={{preferences,setPreferences} satisfies AppContext}/></main></div><ScrollRestoration/></>;
}
export function RouteError(){const error=useRouteError();return <section className="empty-state"><h1>Unable to open this page</h1><p role="alert">{errorMessage(error)}</p><Link className="button secondary" to="/library">Back to library</Link></section>;}
