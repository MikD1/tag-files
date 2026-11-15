import type {ButtonHTMLAttributes,ReactNode} from 'react';
import {Icon} from './Icon';
export function Button({icon,label,children,className='',...props}:ButtonHTMLAttributes<HTMLButtonElement>&{icon?:string;label?:string;children?:ReactNode}){
 return <button type="button" className={`button ${className}`} {...(label?{'aria-label':label,title:label}:{})} {...props}>{icon&&<Icon name={icon}/>} {children}</button>;
}
