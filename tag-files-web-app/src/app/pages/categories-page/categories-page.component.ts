import {Component} from '@angular/core';
import {CategoriesTableComponent} from '../../components/categories-table/categories-table.component';

@Component({
  selector: 'app-categories-page',
  standalone: true,
  imports: [
    CategoriesTableComponent
  ],
  templateUrl: './categories-page.component.html',
  styleUrl: './categories-page.component.scss'
})
export class CategoriesPageComponent {
}
