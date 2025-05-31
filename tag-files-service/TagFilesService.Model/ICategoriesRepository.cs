namespace TagFilesService.Model;

public interface ICategoriesRepository
{
    Task<Category> SaveCategory(Category category);

    Task<List<Category>> GetCategories();

    Task<Category> GetCategory(uint id);

    Task DeleteCategory(uint id);
}