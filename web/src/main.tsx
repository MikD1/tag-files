import React from 'react';
import ReactDOM from 'react-dom/client';
import {CloseButton,MantineProvider,Modal,Tooltip,createTheme} from '@mantine/core';
import {QueryClientProvider} from '@tanstack/react-query';
import {createBrowserRouter,RouterProvider,Navigate,useOutletContext} from 'react-router';
import {App,RouteError,type AppContext} from './App';
import {LibraryPage} from './catalog/LibraryPage';
import {queryClient,itemsQuery} from './api/queries';
import {readLibrary} from './catalog/url';
import {Icon} from './components/Icon';
import '@mantine/core/styles.css';
import './styles.css';

function LibraryRoute(){const {preferences,setPreferences}=useOutletContext<AppContext>();return <LibraryPage preferences={preferences} onPreferences={setPreferences}/>;}
const router=createBrowserRouter([{Component:App,ErrorBoundary:RouteError,HydrateFallback:()=> <p role="status">Loading…</p>,children:[{index:true,element:<Navigate to="/library" replace/>},{path:'library',Component:LibraryRoute,loader:async({request})=>{await queryClient.ensureQueryData(itemsQuery(readLibrary(new URL(request.url))));return null;}},{path:'content/:id',lazy:async()=>({Component:(await import('./viewer')).ViewerPage})},{path:'uploads',lazy:async()=>({Component:(await import('./uploads/UploadsPage')).UploadsPage})},...(['collections','tags','categories'] as const).map(kind=>({path:kind,lazy:async()=>{const {OrganizationPage}=await import('./catalog/OrganizationPage');return {Component:()=> <OrganizationPage kind={kind}/>};}})),{path:'*',element:<section className="empty-state"><h1>Page not found</h1><a className="button secondary" href="/library">Back to library</a></section>}]}]);
const theme=createTheme({
  fontFamily:'-apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
  primaryColor:'indigo',
  defaultRadius:7,
  components:{
    CloseButton:CloseButton.extend({defaultProps:{icon:<Icon name="close"/>}}),
    Modal:Modal.extend({styles:{content:{backgroundColor:'var(--panel)'},header:{backgroundColor:'var(--panel)'}}}),
    Tooltip:Tooltip.extend({styles:{tooltip:{backgroundColor:'var(--panel-raised)',color:'var(--text)'}}}),
  },
});
ReactDOM.createRoot(document.getElementById('root')!).render(<React.StrictMode><MantineProvider forceColorScheme="dark" theme={theme}><QueryClientProvider client={queryClient}><RouterProvider router={router}/></QueryClientProvider></MantineProvider></React.StrictMode>);
