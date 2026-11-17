import type {ButtonHTMLAttributes,ReactNode} from 'react';
import {Icon,type IconName} from './Icon';
export function Button({icon,label,children,className='',...props}:ButtonHTMLAttributes<HTMLButtonElement>&{icon?:IconName;label?:string;children?:ReactNode}){
 return <button type="button" className={`button ${className}`} {...(label?{'aria-label':label,title:label}:{})} {...props}>{icon&&<Icon name={icon}/>} {children}</button>;
}
