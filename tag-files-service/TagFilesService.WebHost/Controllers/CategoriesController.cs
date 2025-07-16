using Microsoft.AspNetCore.Mvc;
using TagFilesService.Model;
using TagFilesService.WebHost.Dto.Category;

namespace TagFilesService.WebHost.Controllers;

[ApiController]
[Route("api/categories")]
public class CategoriesController(ICategoriesRepository categoriesRepository) : ControllerBase
{
    [HttpGet]
    public async Task<ActionResult<List<CategoryDto>>> GetCategories()
    {
        List<Category> categories = await categoriesRepository.GetCategories();
        List<CategoryDto> dto = categories.Select(CategoryDto.FromModel).ToList();
        return Ok(dto);
    }

    [HttpGet("{id}")]
    public async Task<ActionResult<CategoryDto>> GetCategory(uint id)
    {
        Category category = await categoriesRepository.GetCategory(id);
        CategoryDto dto = CategoryDto.FromModel(category);
        return Ok(dto);
    }

    [HttpPost]
    public async Task<ActionResult<CategoryDto>> CreateCategory([FromBody] CreateCategoryDto dto)
    {
        Category category = new(dto.Name, dto.TagQuery, dto.ItemsType);
        await categoriesRepository.SaveCategory(category);
        CategoryDto createdDto = CategoryDto.FromModel(category);
        return Ok(createdDto);
    }

    [HttpPut("{id}")]
    public async Task<ActionResult<CategoryDto>> UpdateCategory(uint id, [FromBody] UpdateCategoryDto dto)
    {
        Category category = await categoriesRepository.GetCategory(id);

        if (dto.Name is not null)
        {
            category.Rename(dto.Name);
        }

        category.UpdateFilter(dto.TagQuery, dto.ItemsType);
        await categoriesRepository.SaveCategory(category);

        CategoryDto updatedDto = CategoryDto.FromModel(category);
        return Ok(updatedDto);
    }

    [HttpDelete("{id}")]
    public async Task<ActionResult> DeleteCategory(uint id)
    {
        await categoriesRepository.DeleteCategory(id);
        return Ok();
    }
}