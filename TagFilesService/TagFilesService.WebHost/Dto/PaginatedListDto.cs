namespace TagFilesService.WebHost.Dto;

public record PaginatedListDto<T>(
    List<T> Items,
    int TotalItems,
    int PageIndex,
    int TotalPages);